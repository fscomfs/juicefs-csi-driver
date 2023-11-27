/*
 Copyright 2022 Juicedata Inc

 Licensed under the Apache License, Version 2.0 (the "License");
 you may not use this file except in compliance with the License.
 You may obtain a copy of the License at

     http://www.apache.org/licenses/LICENSE-2.0

 Unless required by applicable law or agreed to in writing, software
 distributed under the License is distributed on an "AS IS" BASIS,
 WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
 See the License for the specific language governing permissions and
 limitations under the License.
*/

package mutate

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"

	corev1 "k8s.io/api/core/v1"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/client-go/util/retry"
	"k8s.io/klog"

	"github.com/juicedata/juicefs-csi-driver/pkg/config"
	volconf "github.com/juicedata/juicefs-csi-driver/pkg/cv_webhook/handler/config"
	"github.com/juicedata/juicefs-csi-driver/pkg/juicefs"
	"github.com/juicedata/juicefs-csi-driver/pkg/juicefs/mount/builder"
	"github.com/juicedata/juicefs-csi-driver/pkg/k8sclient"
)

type InitContainerMutate struct {
	Client  *k8sclient.K8sClient
	juicefs juicefs.Interface

	Pair       []volconf.PVPair
	jfsSetting *config.JfsSetting
}

var _ Mutate = &InitContainerMutate{}

func NewInitContainerMutate(client *k8sclient.K8sClient, jfs juicefs.Interface, pair []volconf.PVPair) Mutate {
	return &InitContainerMutate{
		Client:  client,
		Pair:    pair,
		juicefs: jfs,
	}
}

func (s *InitContainerMutate) Mutate(ctx context.Context, pod *corev1.Pod) (out *corev1.Pod, err error) {
	out = pod.DeepCopy()
	for i, pair := range s.Pair {
		out, err = s.mutate(ctx, out, pair, i)
		if err != nil {
			return
		}
	}
	return
}

func (s *InitContainerMutate) mutate(ctx context.Context, pod *corev1.Pod, pair volconf.PVPair, index int) (out *corev1.Pod, err error) {
	// get secret, volumeContext and mountOptions from PV
	secrets, volCtx, options, err := s.GetSettings(*pair.PV)
	if err != nil {
		klog.Errorf("get settings from pv %s of pod %s namespace %s err: %v", pair.PV.Name, pod.Name, pod.Namespace, err)
		return
	}

	out = pod.DeepCopy()
	// gen jfs settings
	jfsSetting, err := s.juicefs.Settings(ctx, pair.PV.Spec.CSI.VolumeHandle, secrets, volCtx, options)
	if err != nil {
		return
	}

	jfsSetting.Attr.Namespace = pod.Namespace
	sourcePaths := []string{}
	if subPath, ok := pod.Annotations["subpath-"+pair.PV.Name]; ok {
		sourcePaths = strings.Split(subPath, ";")
	}
	jfsSetting.Attr.SourcePath = sourcePaths
	jfsSetting.SecretName = pair.PVC.Name + "-jfs-secret"
	s.jfsSetting = jfsSetting
	builder := NewBuilder(jfsSetting, pair, secrets)
	// gen init container
	syncWaitContainer := builder.NewSyncWaitContainer()
	containerStr, _ := json.Marshal(syncWaitContainer)
	klog.V(6).Infof("sync wait container: %v\n", string(containerStr))

	// inject initContainer
	s.injectInitContainer(out, *syncWaitContainer)
	// inject label
	s.injectLabel(out)

	return
}

func (s *InitContainerMutate) Deduplicate(pod, mountPod *corev1.Pod, index int) {
	conflict := false
	// deduplicate container name
	for _, c := range pod.Spec.Containers {
		if c.Name == mountPod.Spec.Containers[0].Name {
			mountPod.Spec.Containers[0].Name = fmt.Sprintf("%s-%d", c.Name, index)
			conflict = true
		}
	}
	if !conflict {
		// if container name not conflict, do not check others
		return
	}

	// deduplicate initContainer name
	for _, c := range pod.Spec.InitContainers {
		if c.Name == mountPod.Spec.InitContainers[0].Name {
			mountPod.Spec.InitContainers[0].Name = fmt.Sprintf("%s-%d", c.Name, index)
		}
	}

	// deduplicate volume name
	for i, mv := range mountPod.Spec.Volumes {
		if mv.Name == builder.UpdateDBDirName || mv.Name == builder.JfsDirName || mv.Name == builder.JfsRootDirName {
			continue
		}
		mountIndex := 0
		for j, mm := range mountPod.Spec.Containers[0].VolumeMounts {
			if mm.Name == mv.Name {
				mountIndex = j
				break
			}
		}
		for _, v := range pod.Spec.Volumes {
			if v.Name == mv.Name {
				mountPod.Spec.Volumes[i].Name = fmt.Sprintf("%s-%s", mountPod.Spec.Containers[0].Name, v.Name)
				mountPod.Spec.Containers[0].VolumeMounts[mountIndex].Name = mountPod.Spec.Volumes[i].Name
			}
		}
	}

}

func (s *InitContainerMutate) GetSettings(pv corev1.PersistentVolume) (secrets, volCtx map[string]string, options []string, err error) {
	// get secret
	secret, err := s.Client.GetSecret(
		context.TODO(),
		pv.Spec.CSI.NodePublishSecretRef.Name,
		pv.Spec.CSI.NodePublishSecretRef.Namespace,
	)
	if err != nil {
		return
	}

	secrets = make(map[string]string)
	for k, v := range secret.Data {
		secrets[k] = string(v)
	}
	volCtx = pv.Spec.CSI.VolumeAttributes
	klog.V(5).Infof("volume context of pv %s: %v", pv.Name, volCtx)

	options = []string{}
	if len(pv.Spec.AccessModes) == 1 && pv.Spec.AccessModes[0] == corev1.ReadOnlyMany {
		options = append(options, "ro")
	}
	// get mountOptions from PV.spec.mountOptions
	options = append(options, pv.Spec.MountOptions...)

	mountOptions := []string{}
	// get mountOptions from PV.volumeAttributes
	if opts, ok := volCtx["mountOptions"]; ok {
		mountOptions = strings.Split(opts, ",")
	}
	options = append(options, mountOptions...)
	klog.V(5).Infof("volume options of pv %s: %v", pv.Name, options)

	return
}

func (s *InitContainerMutate) injectInitContainer(pod *corev1.Pod, container corev1.Container) {
	pod.Spec.InitContainers = append([]corev1.Container{container}, pod.Spec.InitContainers...)
}

func (s *InitContainerMutate) injectVolume(pod *corev1.Pod, volumes []corev1.Volume, mountPath string, pair volconf.PVPair) {
	hostMount := filepath.Join(config.MountPointPath, mountPath, s.jfsSetting.SubPath)
	mountedVolume := []corev1.Volume{}
	podVolumes := make(map[string]bool)
	for _, volume := range pod.Spec.Volumes {
		podVolumes[volume.Name] = true
	}
	for _, v := range volumes {
		if v.Name == builder.UpdateDBDirName || v.Name == builder.JfsDirName || v.Name == builder.JfsRootDirName {
			if _, ok := podVolumes[v.Name]; ok {
				continue
			}
		}
		mountedVolume = append(mountedVolume, v)
	}
	for i, volume := range pod.Spec.Volumes {
		if volume.PersistentVolumeClaim != nil && volume.PersistentVolumeClaim.ClaimName == pair.PVC.Name {
			// overwrite original volume and use juicefs volume mountpoint instead
			pod.Spec.Volumes[i] = corev1.Volume{
				Name: volume.Name,
				VolumeSource: corev1.VolumeSource{
					HostPath: &corev1.HostPathVolumeSource{
						Path: hostMount,
					},
				}}
		}
	}
	// inject volume
	pod.Spec.Volumes = append(pod.Spec.Volumes, mountedVolume...)
}

func (s *InitContainerMutate) injectLabel(pod *corev1.Pod) {
	metaObj := pod.ObjectMeta
	if metaObj.Labels == nil {
		metaObj.Labels = map[string]string{}
	}

	metaObj.Labels[config.CvInjectInitContainerDone] = config.True
	metaObj.DeepCopyInto(&pod.ObjectMeta)
}

func (s *InitContainerMutate) createOrUpdateSecret(ctx context.Context, secret *corev1.Secret) error {
	klog.V(5).Infof("createOrUpdateSecret: %s, %s", secret.Name, secret.Namespace)
	err := retry.RetryOnConflict(retry.DefaultBackoff, func() error {
		oldSecret, err := s.Client.GetSecret(ctx, secret.Name, secret.Namespace)
		if err != nil {
			if k8serrors.IsNotFound(err) {
				// secret not exist, create
				_, err := s.Client.CreateSecret(ctx, secret)
				return err
			}
			// unexpected err
			return err
		}

		oldSecret.StringData = secret.StringData
		return s.Client.UpdateSecret(ctx, oldSecret)
	})
	if err != nil {
		klog.Errorf("createOrUpdateSecret: secret %s: %v", secret.Name, err)
		return err
	}
	return nil
}
