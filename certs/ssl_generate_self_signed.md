# Way 1: CFSSL to generate certificates

More about [CFSSL here]("https://github.com/cloudflare/cfssl")

```bash
cd kubernetes\admissioncontrollers\introduction

docker run -it --rm -v ${PWD}:/work -w /work debian bash

apt-get update && apt-get install -y curl &&
curl -L https://github.com/cloudflare/cfssl/releases/download/v1.5.0/cfssl_1.5.0_linux_amd64 -o /usr/local/bin/cfssl && \
curl -L https://github.com/cloudflare/cfssl/releases/download/v1.5.0/cfssljson_1.5.0_linux_amd64 -o /usr/local/bin/cfssljson && \
chmod +x /usr/local/bin/cfssl && \
chmod +x /usr/local/bin/cfssljson
```

#### generate ca in /tmp
```bash
cfssl gencert -initca ./tls/ca-csr.json | cfssljson -bare /tmp/ca
```

#### generate certificate in /tmp

```bash
cfssl gencert \
  -ca=/tmp/ca.pem \
  -ca-key=/tmp/ca-key.pem \
  -config=./tls/ca-config.json \
  -hostname="example-webhook,example-webhook.default.svc.cluster.local,example-webhook.default.svc,localhost,127.0.0.1" \
  -profile=default \
  ./tls/ca-csr.json | cfssljson -bare /tmp/example-webhook
```

# Way 2: Self Signed Certificate
use Self Signed Certificate

This tool generates self signed certificates that can be used with Kubernetes webhook servers.

#### Install

```bash
git clone https://github.com/surajssd/self-signed-cert
go install
```

Now the binary `self-signed-cert` will be built and available in you `GOBIN` directory.

#### Usage

```bash
self-signed-cert --namespace <k8s namespace> --service-name <k8s service name>
```

# make a secret
```bash
cat <<EOF > ./tls/secret-webhook-tls.yaml
apiVersion: v1
kind: Secret
metadata:
  name: example-webhook-tls
type: Opaque
data:
  tls.crt: $(cat server.crt | base64 | tr -d '\n')
  tls.key: $(cat server.key | base64 | tr -d '\n') 
EOF
```

# generate CA Bundle + inject into template
```bash
ca_pem_b64="$(openssl base64 -A <"ca.crt")"

sed -e 's@${CA_PEM_B64}@'"$ca_pem_b64"'@g' <"validating-webhook.yaml" \
    > webhook.yaml
```

