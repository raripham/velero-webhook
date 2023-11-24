package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"

	"github.com/rs/zerolog/log"
	velerov1 "github.com/vmware-tanzu/velero/pkg/apis/velero/v1"
	admission "k8s.io/api/admission/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/runtime/serializer"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/client-go/util/homedir"
)

var (
	runtimeScheme = runtime.NewScheme()
	codecFactory  = serializer.NewCodecFactory(runtimeScheme)
	deserializer  = codecFactory.UniversalDeserializer()
)

// add kind AdmissionReview in scheme
func init() {
	// _ = corev1.AddToScheme(runtimeScheme)
	_ = admission.AddToScheme(runtimeScheme)
	// _ = v1.AddToScheme(runtimeScheme)
}

type admitv1Func func(admission.AdmissionReview) *admission.AdmissionResponse

type admitHandler struct {
	v1 admitv1Func
}

func AdmitHandler(f admitv1Func) admitHandler {
	return admitHandler{
		v1: f,
	}
}

type patchOperation struct {
	Op    string      `json:"op"`
	Path  string      `json:"path"`
	Value interface{} `json:"value"`
}

var namespace string

// serve handles the http portion of a request prior to handing to an admit
// function
func serve(w http.ResponseWriter, r *http.Request, admit admitHandler) {
	var body []byte
	if r.Body != nil {
		if data, err := ioutil.ReadAll(r.Body); err == nil {
			body = data
		}
	}

	// verify the content type is accurate
	contentType := r.Header.Get("Content-Type")
	if contentType != "application/json" {
		// slog.Error("contentType=%s, expect application/json", contentType)
		log.Error().Msgf("contentType=%s, expect application/json", contentType)
		return
	}
	// slog.Info("handling request: %s", body)
	log.Info().Msgf("handling request: %s", body)
	var admiss admission.AdmissionReview
	json.Unmarshal(body, &admiss)
	namespace = admiss.Request.Namespace
	log.Info().Msgf("namespace:", namespace)
	var responseObj runtime.Object
	if obj, gvk, err := deserializer.Decode(body, nil, nil); err != nil {
		msg := fmt.Sprintf("Request could not be decoded: %v", err)
		log.Error().Msg(msg)
		// slog.Error(msg)
		http.Error(w, msg, http.StatusBadRequest)
		return

	} else {
		requestedAdmissionReview, ok := obj.(*admission.AdmissionReview)
		if !ok {
			slog.Error("Expected v1.AdmissionReview but got: %T", obj)
			// log.Error().Msgf("Expected v1.AdmissionReview but got: %T", obj)
			return
		}
		responseAdmissionReview := &admission.AdmissionReview{}
		responseAdmissionReview.SetGroupVersionKind(*gvk)
		responseAdmissionReview.Response = admit.v1(*requestedAdmissionReview)
		responseAdmissionReview.Response.UID = requestedAdmissionReview.Request.UID
		responseObj = responseAdmissionReview

	}
	// slog.Info("sending response: %v", responseObj)
	log.Info().Msgf("sending response: %v", responseObj)
	respBytes, err := json.Marshal(responseObj)
	if err != nil {
		log.Err(err)
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	if _, err := w.Write(respBytes); err != nil {
		log.Err(err)
		// log.Fatal(err)
	}
}

func serveValidate(w http.ResponseWriter, r *http.Request) {
	serve(w, r, AdmitHandler(validate))
}

// Hide backups old version on argocd
// verify if a Deployment has the 'prod' prefix name
func validate(ar admission.AdmissionReview) *admission.AdmissionResponse {
	log.Info().Msgf("validating backups.velero")
	// slog.Info("validating backups.velero")
	backupVeleroResource := metav1.GroupVersionResource{Group: "velero.io", Version: "v1", Resource: "backups"}
	if ar.Request.Resource != backupVeleroResource {
		// slog.Error("expect resource to be %s", backupVeleroResource)
		log.Error().Msgf("expect resource to be %s", backupVeleroResource)
		return nil
	}

	var backupList []string
	backupList = BackupList()
	// log.Info().Msgf("number of backups need to be hidden: %d", len(backupList))
	if len(backupList) >= 1 {
		log.Info().Msgf("number of backups need to be hidden: %d", len(backupList))
		for i := 0; i < len(backupList); i++ {
			backup, err := HideResourceArgocd(backupList[i])
			if err != nil {
				log.Err(err)
			} else {
				// slog.Info("Hide %s backup from argocd successfully!", backup.Name)
				log.Info().Msgf("Hide %s backup from argocd successfully!", backup.Name)
			}
		}
	} else {
		log.Info().Msgf("no backups need to be hidden")
	}
	raw := ar.Request.Object.Raw
	backup := velerov1.Backup{}
	if _, _, err := deserializer.Decode(raw, nil, &backup); err != nil {
		log.Err(err)
		return &admission.AdmissionResponse{
			Result: &metav1.Status{
				Message: err.Error(),
			},
		}
	}
	return &admission.AdmissionResponse{Allowed: true}
}
func BackupList() []string {
	config, err := buildConfiguration()
	if err != nil {
		log.Err(err)
	}

	clientSet, err := kubernetes.NewForConfig(config)
	if err != nil {
		log.Err(err)
	}

	var backups velerov1.BackupList
	group := "velero.io"
	version := "v1"
	plural := "backups"
	list, err := clientSet.RESTClient().Get().AbsPath(
		fmt.Sprintf("/apis/%s/%s/namespaces/%s/%s",
			group,
			version,
			namespace,
			plural,
		)).DoRaw(context.TODO())
	if err != nil {
		log.Err(err)
	}
	json.Unmarshal(list, &backups)

	var BackupDetachList []string
	log.Info().Msgf("List of Velero Backups display on argocd:")
	for _, backup := range backups.Items {
		if backup.Labels["argocd.argoproj.io/instance"] != "" && backup.Namespace == namespace {
			BackupDetachList = append(BackupDetachList, backup.Name)
		}
	}
	return BackupDetachList
}

func HideResourceArgocd(backupName string) (velerov1.Backup, error) {
	var patches []patchOperation
	var backups velerov1.Backup

	config, err := buildConfiguration()
	if err != nil {
		log.Err(err)
	}
	client, err := dynamic.NewForConfig(config)

	patches = append(patches, patchOperation{
		Op:    "remove",
		Path:  "/metadata/labels/argocd.argoproj.io~1instance",
		Value: "",
	})
	patches = append(patches, patchOperation{
		Op:   "replace",
		Path: "/metadata/ownerReferences",
	})
	patchesBytes, err := json.Marshal(patches)
	if err != nil {
		log.Err(err)
	}

	backupVeleroResource := schema.GroupVersionResource{
		Group:    "velero.io",
		Version:  "v1",
		Resource: "backups",
	}

	result, err := client.Resource(backupVeleroResource).
		Namespace(namespace).
		Patch(context.TODO(), backupName, types.JSONPatchType, patchesBytes, metav1.PatchOptions{})
	if err != nil {
		log.Err(err)
	}

	resultBytes, err := json.Marshal(result)
	if err != nil {
		log.Err(err)
	}

	json.Unmarshal(resultBytes, &backups)
	return backups, err
}

func buildConfiguration() (*rest.Config, error) {
	var config *rest.Config

	useKubeConfig := os.Getenv("USE_KUBECONFIG")
	kubeConfigFilePath := os.Getenv("KUBECONFIG")

	if len(useKubeConfig) == 0 {
		// default to service account in cluster token
		c, err := rest.InClusterConfig()
		if err != nil {
			log.Err(err)
			return nil, err
		}
		config = c
	} else {
		var kubeconfig string
		if kubeConfigFilePath == "" {
			if home := homedir.HomeDir(); home != "" {
				kubeconfig = filepath.Join(home, ".kube", "config")
			}
		} else {
			kubeconfig = kubeConfigFilePath
		}
		fmt.Println("kubeconfig: " + kubeconfig)

		c, err := clientcmd.BuildConfigFromFlags("", kubeconfig)
		if err != nil {
			log.Err(err)
			return nil, err
		}
		config = c
	}

	return config, nil
}
