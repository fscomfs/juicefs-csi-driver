# Copyright 2022 Juicedata Inc
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
ARG JUICEFS_MOUNT_IMAGE
FROM $JUICEFS_MOUNT_IMAGE as juicebinary

FROM golang:1.20-buster as builder

ARG GOPROXY
ARG JUICEFS_REPO_BRANCH=cvmart-main
ARG JUICEFS_REPO_REF=${JUICEFS_REPO_BRANCH}
ARG JUICEFS_CSI_REPO_REF=v0.22.1-cvmart
ENV GOPROXY=${GOPROXY:-https://proxy.golang.org}
WORKDIR /workspace
COPY . .
RUN make
ENV STATIC=1

FROM alpine:3.15.5

ARG JUICEFS_MOUNT_IMAGE
ENV JUICEFS_MOUNT_IMAGE=${JUICEFS_MOUNT_IMAGE}
RUN mkdir -p /other_tool
COPY --from=builder /workspace/bin/juicefs-csi-driver /usr/local/bin/juicefs-csi-driver
COPY --from=builder /workspace/other_tool/ /other_tool/
COPY --from=juicebinary /usr/local/bin/juicefs /usr/local/bin/juicefs
RUN ln -s /usr/local/bin/juicefs /bin/mount.juicefs
RUN apk add --no-cache tini
RUN /bin/sh -c 'if [ "$TARGETARCH" = "amd64" ]; then \
        cp /other_tool/fuse-overlayfs-x86_64 /usr/bin/fuse-overlayfs  && chmod +x /usr/bin/fuse-overlayfs; \
    else \
        cp /other_tool/fuse-overlayfs-aarch64 /usr/bin/fuse-overlayfs  && chmod +x /usr/bin/fuse-overlayfs; \
       fi' \
ENV unset https_proxy
ENV unset http_proxy
ENTRYPOINT ["/sbin/tini", "--", "juicefs-csi-driver"]
