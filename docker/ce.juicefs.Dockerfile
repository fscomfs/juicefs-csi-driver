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
ARG REGISTRY
ARG CE_VERSION
ARG BASE_VERSION
FROM $REGISTRY/fscomfs/mount:${BASE_VERSION}-base-build as binaryimage
ARG GOPROXY
ARG TARGETARCH
ARG JUICEFS_REPO_URL=https://github.com/fscomfs/juicefs
ARG JUICEFS_REPO_BRANCH=main
ARG JUICEFS_REPO_REF=${JUICEFS_REPO_REF}
WORKDIR /workspace
ENV GOPROXY=${GOPROXY:-https://proxy.golang.org}
ENV https_proxy=${HTTPS_PROXY}
ENV http_proxy=${HTTPS_PROXY}
RUN cd /workspace && git clone --branch=$JUICEFS_REPO_BRANCH $JUICEFS_REPO_URL && \
    cd juicefs && git checkout $JUICEFS_REPO_REF && go get github.com/ceph/go-ceph@v0.4.0 && go mod tidy && \
    bash -c "if [[ ${TARGETARCH} == amd64 ]]; then make juicefs.all && mv juicefs.all juicefs && upx juicefs; else make juicefs.ceph && mv juicefs.ceph juicefs; fi" && \
    mv juicefs /usr/local/bin/juicefs
ENV https_proxy=""
ENV http_proxy=""
# ----------
ARG REGISTRY
ARG CE_VERSION
FROM $REGISTRY/fscomfs/mount:${BASE_VERSION}-base
ARG TARGETARCH
COPY --from=binaryimage /usr/local/bin/juicefs /usr/local/bin/juicefs
RUN ln -s /usr/local/bin/juicefs /bin/mount.juicefs && /usr/local/bin/juicefs --version
ENV https_proxy=""
ENV http_proxy=""
