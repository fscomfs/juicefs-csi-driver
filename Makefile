# Copyright 2018 The Kubernetes Authors.
#
# Licensed under the Apache License, Version 2.0 (the "License");
# you may not use this file except in compliance with the License.
# You may obtain a copy of the License at
#
#     http://www.apache.org/licenses/LICENSE-2.0
#
# Unless required by applicable law or agreed to in writing, software
# distributed under the License is distributed on an "AS IS" BASIS,
# WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
# See the License for the specific language governing permissions and
# limitations under the License.

IMAGE?=juicedata/juicefs-csi-driver
REGISTRY?=docker.io
TARGETARCH?=amd64
VERSION=$(shell git describe --tags --match 'v*' --always --dirty)
GIT_BRANCH?=$(shell git rev-parse --abbrev-ref HEAD)
GIT_COMMIT?=$(shell git rev-parse HEAD)
DEV_TAG=dev-$(shell git describe --always --dirty)
BUILD_DATE?=$(shell date -u +"%Y-%m-%dT%H:%M:%SZ")
HPROXY?=http://192.168.67.70:7890
PKG=github.com/juicedata/juicefs-csi-driver
LDFLAGS?="-X ${PKG}/pkg/driver.driverVersion=${VERSION} -X ${PKG}/pkg/driver.gitCommit=${GIT_COMMIT} -X ${PKG}/pkg/driver.buildDate=${BUILD_DATE} -s -w"
GO111MODULE=on

GOPROXY=https://goproxy.io
GOPATH=$(shell go env GOPATH)
GOOS=$(shell go env GOOS)
GOBIN=$(shell pwd)/bin

.PHONY: juicefs-csi-driver
juicefs-csi-driver:
	mkdir -p bin
	CGO_ENABLED=0 GOOS=linux go build -ldflags ${LDFLAGS} -o bin/juicefs-csi-driver ./cmd/

.PHONY: verify
verify:
	./hack/verify-all

.PHONY: test
test:
	go test -gcflags=all=-l -v -race -cover ./pkg/... -coverprofile=cov1.out

.PHONY: test-sanity
test-sanity:
	go test -v -cover ./tests/sanity/... -coverprofile=cov2.out

# build deploy yaml
yaml:
	echo "# DO NOT EDIT: generated by 'kustomize build'" > deploy/k8s.yaml
	kustomize build deploy/kubernetes/release >> deploy/k8s.yaml
	cp deploy/k8s.yaml deploy/k8s_before_v1_18.yaml
	sed -i.orig 's@storage.k8s.io/v1@storage.k8s.io/v1beta1@g' deploy/k8s_before_v1_18.yaml

	echo "# DO NOT EDIT: generated by 'kustomize build'" > deploy/webhook.yaml
	kustomize build deploy/kubernetes/webhook >> deploy/webhook.yaml
	echo "# DO NOT EDIT: generated by 'kustomize build'" > deploy/webhook-with-certmanager.yaml
	kustomize build deploy/kubernetes/webhook-with-certmanager >> deploy/webhook-with-certmanager.yaml
	kustomize build deploy/kubernetes/cv-webhook-with-certmanager >> deploy/cv-webhook-with-certmanager.yaml
	./hack/update_install_script.sh

.PHONY: deploy
deploy: yaml
	kubectl apply -f $<

.PHONY: deploy-delete
uninstall: yaml
	kubectl delete -f $<

# build dev image
.PHONY: image-dev
image-dev: juicefs-csi-driver
	docker build --build-arg TARGETARCH=$(TARGETARCH) -t $(IMAGE):$(DEV_TAG) --build-arg=ttps_proxy=${HPROXY} -f docker/dev.Dockerfile bin

# push dev image
.PHONY: push-dev
push-dev:
ifeq ("$(DEV_K8S)", "microk8s")
	docker image save -o juicefs-csi-driver-$(DEV_TAG).tar $(IMAGE):$(DEV_TAG)
	sudo microk8s.ctr image import juicefs-csi-driver-$(DEV_TAG).tar
	rm -f juicefs-csi-driver-$(DEV_TAG).tar
else ifeq ("$(DEV_K8S)", "kubeadm")
	docker tag $(IMAGE):$(DEV_TAG) $(DEV_REGISTRY):$(DEV_TAG)
	docker push $(DEV_REGISTRY):$(DEV_TAG)
else
	minikube cache add $(IMAGE):$(DEV_TAG)
endif

.PHONY: deploy-dev/kustomization.yaml
deploy-dev/kustomization.yaml:
	mkdir -p $(@D)
	touch $@
	cd $(@D); kustomize edit add resource ../deploy/kubernetes/cv-webhook;
ifeq ("$(DEV_K8S)", "kubeadm")
	cd $(@D); kustomize edit set image juicedata/juicefs-csi-driver=$(DEV_REGISTRY):$(DEV_TAG)
else
	cd $(@D); kustomize edit set image juicedata/juicefs-csi-driver=:$(DEV_TAG)
endif

deploy-dev/k8s.yaml: deploy-dev/kustomization.yaml deploy/kubernetes/release/*.yaml
	echo "# DO NOT EDIT: generated by 'kustomize build $(@D)'" > $@
	kustomize build $(@D) >> $@
	# Add .orig suffix only for compactiblity on macOS
	./hack/dev_update_cv_install_script.sh
	./scripts/juicefs-csi-cv-webhook-install.sh gen-dev
ifeq ("$(DEV_K8S)", "microk8s")
	sed -i 's@/var/lib/kubelet@/var/snap/microk8s/common/var/lib/kubelet@g' $@
endif
ifeq ("$(DEV_K8S)", "kubeadm")
	sed -i.orig 's@juicedata/juicefs-csi-driver.*$$@$(DEV_REGISTRY):$(DEV_TAG)@g' $@
else
	sed -i.orig 's@juicedata/juicefs-csi-driver.*$$@juicedata/juicefs-csi-driver:$(DEV_TAG)@g' $@
endif


.PHONY: deploy-dev
deploy-dev: deploy-dev/k8s.yaml
	kapp deploy --app juicefs-csi-driver --file $<

.PHONY: delete-dev
delete-dev: deploy-dev/k8s.yaml
	kapp delete --app juicefs-csi-driver

.PHONY: install-dev
install-dev: verify test image-dev push-dev deploy-dev/k8s.yaml deploy-dev

bin/mockgen: | bin
	go install github.com/golang/mock/mockgen@v1.5.0

mockgen: bin/mockgen
	./hack/update-gomock

npm-install:
	npm install
	npm ci

check-docs:
	npm run markdown-lint
	autocorrect --fix ./docs/
	npm run check-broken-link
