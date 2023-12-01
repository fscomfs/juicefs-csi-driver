package mutate

import (
	"github.com/juicedata/juicefs-csi-driver/pkg/config"
	volconf "github.com/juicedata/juicefs-csi-driver/pkg/cv_webhook/handler/config"
	"github.com/juicedata/juicefs-csi-driver/pkg/util"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/klog"
	"regexp"
	"strconv"
	"strings"
)

type Builder struct {
	jfsSetting *config.JfsSetting
	pair       volconf.PVPair
	secrets    map[string]string
}

func NewBuilder(setting *config.JfsSetting, pair volconf.PVPair, secrets map[string]string) *Builder {
	return &Builder{jfsSetting: setting, pair: pair, secrets: secrets}
}

func (r *Builder) NewSyncWaitContainer() *corev1.Container {
	cmd := r.getCommand()

	container := r.generateInitContainer()

	container.Command = []string{"sh", "-c", cmd}

	return container
}
func (r *Builder) getCommand() string {
	cmd := ""
	klog.V(5).Infof("SyncWait: syncWait %v", r.jfsSetting.Attr.SourcePath)
	syncArgs := []string{"juicefs-csi-driver", "syncWait", "--dst-pv", r.pair.PV.Name}
	for _, s := range r.jfsSetting.Attr.SourcePath {
		syncArgs = append(syncArgs, "--source-path", s)
	}
	cmd = strings.Join(syncArgs, " ")
	return util.QuoteForShell(cmd)
}

//func (r *Builder) getCommand() string {
//	cmd := ""
//	klog.V(5).Infof("Sync: sync %v at %v", util.StripPasswd(r.jfsSetting.Source), r.jfsSetting.MountPath)
//	syncArgs := []string{config.CeMountPath, "myfs=${metaurl}", "sync",
//		fmt.Sprint("s3://%s:%s@%s/%s", r.jfsSetting.CentralAccessKey, r.jfsSetting.CentralSecretKey,
//			strings.TrimRight(r.jfsSetting.CentralBucket, "/"), path.Join(r.jfsSetting.Attr.SyncSrcPath, "/"),
//			fmt.Sprintf("jfs://myfs", path.Join("/", r.jfsSetting.SubPath, r.jfsSetting.Attr.SyncDstPath, "/"))),
//	}
//	options := r.jfsSetting.Options
//	if !util.ContainsPrefix(options, "metrics=") {
//		if r.jfsSetting.Attr.HostNetwork {
//			// Pick up a random (useable) port for hostNetwork MountPods.
//			options = append(options, "metrics=0.0.0.0:0")
//		} else {
//			options = append(options, "metrics=0.0.0.0:9567")
//		}
//	}
//	syncArgs = append(syncArgs, "-o", strings.Join(options, ","))
//	cmd = strings.Join(syncArgs, " ")
//	return util.QuoteForShell(cmd)
//}

func (r *Builder) getMetricsPort() int32 {
	port := int64(9567)
	options := r.jfsSetting.Options

	for _, option := range options {
		if strings.HasPrefix(option, "metrics=") {
			re := regexp.MustCompile(`metrics=.*:([0-9]{1,6})`)
			match := re.FindStringSubmatch(option)
			if len(match) > 0 {
				port, _ = strconv.ParseInt(match[1], 10, 32)
			}
		}
	}

	return int32(port)
}
func (r *Builder) generateInitContainer() *corev1.Container {
	isPrivileged := false
	rootUser := int64(0)
	return &corev1.Container{
		Name:  config.SyncWaitContainerName,
		Image: r.jfsSetting.Attr.SyncWaitImage,
		SecurityContext: &corev1.SecurityContext{
			Privileged: &isPrivileged,
			RunAsUser:  &rootUser,
		},
		Env: []corev1.EnvVar{
			{Name: "CONTROLLER_URL", Value: r.secrets[config.SyncController]},
			{Name: "CONTROLLER_NAMESPACE", Value: config.Namespace},
		},
	}
}
