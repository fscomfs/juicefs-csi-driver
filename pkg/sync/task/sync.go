package task

import (
	"fmt"
	"github.com/juicedata/juicefs-csi-driver/pkg/config"
	"github.com/juicedata/juicefs-csi-driver/pkg/util"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"path"
	"strings"
)

type PlatformDataSync struct {
	PodName           string
	SecretName        string
	PVName            string
	NameSpace         string
	ControllerURL     string
	SourceStorage     string
	SourcePath        string
	SourceBucket      string
	SourceAccessKey   string
	SourceSecretKey   string
	MetaUrl           string
	DstFileSystemName string
	SubDir            string
	Envs              map[string]string
}

func (p *PlatformDataSync) NewSyncPod() *corev1.Pod {
	Image := config.CEMountImage
	cmd := p.buildCmd()
	var envs []corev1.EnvVar
	if p.Envs != nil {
		for s := range p.Envs {
			envs = append(envs, corev1.EnvVar{
				Name:  s,
				Value: p.Envs[s],
			})
		}
	}
	return &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:        p.PodName,
			Namespace:   p.NameSpace,
			Labels:      map[string]string{config.SyncPodLabelKey: config.SyncPodLabelVal, config.SyncLabelPVKey: p.PVName},
			Annotations: map[string]string{config.SyncPodAnnotationSourcePath: p.SourcePath},
		},
		Spec: corev1.PodSpec{
			RestartPolicy: corev1.RestartPolicyNever,
			Containers: []corev1.Container{
				{
					Command:         []string{"sh", "-c", cmd},
					ImagePullPolicy: corev1.PullIfNotPresent,
					Image:           Image,
					Name:            config.SyncContainerName,
					Env:             envs,
					EnvFrom: []corev1.EnvFromSource{{
						SecretRef: &corev1.SecretEnvSource{
							LocalObjectReference: corev1.LocalObjectReference{
								Name: p.SecretName,
							},
						},
					}},
				},
			},
			Affinity: &corev1.Affinity{
				NodeAffinity: &corev1.NodeAffinity{
					PreferredDuringSchedulingIgnoredDuringExecution: []corev1.PreferredSchedulingTerm{{
						Weight: 1,
						Preference: corev1.NodeSelectorTerm{
							MatchExpressions: []corev1.NodeSelectorRequirement{
								{Key: "dataset", Operator: corev1.NodeSelectorOpIn, Values: []string{"1"}},
							},
						},
					}},
				},
			},
		},
	}
}
func (p *PlatformDataSync) buildCmd() string {
	cmd := ""
	syncArgs := []string{"myfs=${metaurl}", config.CeCliPath, "sync",
		fmt.Sprintf("%s://%s:%s@%s/%s/", p.SourceStorage, p.SourceAccessKey, p.SourceSecretKey, p.SourceBucket, strings.Trim(p.SourcePath, "/")),
		fmt.Sprintf("jfs://myfs%s/", path.Join("/", p.SubDir, p.SourcePath)),
		"--report-process-addr", p.ControllerURL, "--pv", p.PVName, "--task-key", p.SourcePath, "--update", "--delete-dst",
	}
	cmd = strings.Join(syncArgs, " ")
	return util.QuoteForShell(cmd)
}