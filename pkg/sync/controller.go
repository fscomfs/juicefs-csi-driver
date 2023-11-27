package sync

import (
	"context"
	"fmt"
	"github.com/juicedata/juicefs-csi-driver/pkg/config"
	"github.com/juicedata/juicefs-csi-driver/pkg/k8sclient"
	k8s "github.com/juicedata/juicefs-csi-driver/pkg/k8sclient"
	"github.com/juicedata/juicefs-csi-driver/pkg/sync/task"
	"github.com/juicedata/juicefs-csi-driver/pkg/util"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	corev1 "k8s.io/api/core/v1"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"k8s.io/klog"
	"net/http"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/cache"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	"sigs.k8s.io/controller-runtime/pkg/source"
	"time"
)

type DataSyncController struct {
	mixture    string
	k8sClient  *k8s.K8sClient
	discoverys map[string]*task.Discovery
	mgr        ctrl.Manager
	ctx        context.Context
}

type PVManage struct {
	SyncController *DataSyncController
}
type SecretManage struct {
	SyncController *DataSyncController
}

type SyncPodManage struct {
	SyncController *DataSyncController
}

var (
	scheme         = runtime.NewScheme()
	syncController *DataSyncController
)

func init() {
	utilruntime.Must(corev1.AddToScheme(scheme))
	utilruntime.Must(clientgoscheme.AddToScheme(scheme))
}
func NewDataSyncController(ctx context.Context,
	leaderElection bool,
	leaderElectionNamespace string,
	leaderElectionLeaseDuration time.Duration) (*DataSyncController, error) {
	conf, err := ctrl.GetConfig()
	if err != nil {
		return nil, err
	}
	mgr, err := ctrl.NewManager(conf, ctrl.Options{
		Scheme:                  scheme,
		Port:                    9443,
		MetricsBindAddress:      "0.0.0.0:8084",
		LeaderElection:          leaderElection,
		LeaderElectionNamespace: leaderElectionNamespace,
		LeaderElectionID:        "sync.juicefs.com",
		LeaseDuration:           &leaderElectionLeaseDuration,
		NewCache: cache.BuilderWithOptions(cache.Options{
			Scheme: scheme,
			SelectorsByObject: cache.SelectorsByObject{
				&corev1.PersistentVolume{}: {
					Label: labels.SelectorFromSet(labels.Set{config.SyncPVLabelKey: config.SyncPVLabelVal}),
				},
				&corev1.Pod{}: {
					Label: labels.SelectorFromSet(labels.Set{config.SyncPodLabelKey: config.SyncPodLabelVal}),
				},
				&corev1.Secret{}: {
					Label: labels.SelectorFromSet(labels.Set{config.SyncPVLabelKey: config.SyncPVLabelVal}),
				},
			},
		}),
	})
	if err != nil {
		klog.Errorf("new sync controller manage error:%v", err)
		return nil, err
	}
	// gen k8s client
	k8sClient, err := k8sclient.NewClient()
	if err != nil {
		klog.V(5).Infof("Could not create k8s client %v", err)
		return nil, err
	}
	syncController = &DataSyncController{
		mgr:       mgr,
		k8sClient: k8sClient,
		ctx:       ctx,
		mixture:   config.SyncController,
	}
	return syncController, err
}
func (c *DataSyncController) Run(ctx context.Context) {
	wrapRegister(c.mixture)

	go func() {
		StartProcessManage()
	}()

	labelSelector := &metav1.LabelSelector{
		MatchLabels: labels.Set{
			config.SyncPVLabelKey: config.SyncPVLabelVal,
		},
	}
	juicefsPVs, err := c.k8sClient.CoreV1().PersistentVolumes().List(context.Background(),
		metav1.ListOptions{
			LabelSelector: labels.Set(labelSelector.MatchLabels).String(),
		})
	if err != nil {
		return
	}
	if c.discoverys == nil {
		c.discoverys = make(map[string]*task.Discovery)
	}
	for i := range juicefsPVs.Items {
		pv := juicefsPVs.Items[i]
		secretName := pv.Spec.CSI.NodePublishSecretRef.Name
		secretNamespace := pv.Spec.CSI.NodePublishSecretRef.Namespace
		secret, err := c.k8sClient.GetSecret(context.Background(), secretName, secretNamespace)
		if err != nil {
			klog.Errorf("sync manager start error: %v", err)
			continue
		}
		if _, ok := c.discoverys[pv.Name]; !ok {
			if secret.Data[config.PlatformController] != nil && secret.Data[config.CentralBucket] != nil {
				dis, err := task.NewDiscover(c.ctx, *secret, c.mixture, pv, c.k8sClient)
				if err != nil {
					continue
				}
				c.discoverys[pv.Name] = dis
			}
		}
	}
	if err := c.SetupWithManager(c.mgr); err != nil {
		klog.Errorf("Register sync controller error: %v", err)
		return
	}
	if err := c.mgr.Start(ctx); err != nil {
		klog.Errorf("sync manager start error: %v", err)
		return
	}

}
func (m *SyncPodManage) Reconcile(ctx context.Context, request reconcile.Request) (reconcile.Result, error) {
	pod, err := m.SyncController.k8sClient.GetPod(ctx, request.Name, request.Namespace)
	if err != nil && !k8serrors.IsNotFound(err) {
		klog.Errorf("sync pv not fount pv: %v error: %v", request.Name, err)
		return reconcile.Result{}, err
	}
	if pod == nil {
		klog.V(6).Infof("sync pod %s has been deleted.", request.Name)
		return reconcile.Result{}, nil
	}
	if !util.IsPodError(pod) && !util.IsPodResourceError(pod) {
		if pvName, ok := pod.Labels[config.SyncLabelPVKey]; ok {
			if pvDiscovery, ok := m.SyncController.discoverys[pvName]; ok {
				sourcePath := pod.Labels[config.SyncPodAnnotationSourcePath]
				if util.IsPodRunning(pod) {
					pvDiscovery.UpdateSourceSyncStatus(sourcePath, task.SYNC_DOING)
					klog.V(5).Infof("sync source path %s syncing", sourcePath)
				}
				if util.IsPodCompleted(pod) {
					pvDiscovery.UpdateSourceSyncStatus(sourcePath, task.SYNC_COMPLETED)
					klog.V(5).Infof("sync source path %s completed", sourcePath)
				}
				if util.IsPodError(pod) {
					pvDiscovery.UpdateSourceSyncStatus(sourcePath, task.SYNC_FAIL)
					klog.V(5).Infof("sync source path %s fail reason:%s", sourcePath, util.PodErrorMessage(pod))
				}
			}
		}
	}
	return reconcile.Result{}, nil
}
func (m *SecretManage) Reconcile(ctx context.Context, request reconcile.Request) (reconcile.Result, error) {
	secret, err := m.SyncController.k8sClient.GetSecret(ctx, request.Name, request.Namespace)
	if err != nil && !k8serrors.IsNotFound(err) {
		klog.Errorf("sync pv not fount pv: %v error: %v", request.Name, err)
		return reconcile.Result{}, err
	}
	if secret == nil {
		klog.V(6).Infof("secret %s has been deleted.", request.Name)
		return reconcile.Result{}, nil
	}
	for s := range m.SyncController.discoverys {
		if m.SyncController.discoverys[s].Secret.Name == secret.Name && m.SyncController.discoverys[s].Secret.Namespace == secret.Namespace {
			m.SyncController.discoverys[s].UpdateDiscovery(*secret)
		}
	}
	return reconcile.Result{}, nil
}
func (m *PVManage) Reconcile(ctx context.Context, request reconcile.Request) (reconcile.Result, error) {
	pv, err := m.SyncController.k8sClient.GetPersistentVolume(ctx, request.Name)
	if err != nil && !k8serrors.IsNotFound(err) {
		klog.Errorf("sync pv not fount pv: %v error: %v", request.Name, err)
		return reconcile.Result{}, err
	}
	if pv == nil {
		klog.Errorf("pv %v is deleted", request.Name)
		if d, ok := m.SyncController.discoverys[request.Name]; ok {
			d.Done()
		}
		return reconcile.Result{}, err
	}
	secretName := pv.Spec.CSI.NodePublishSecretRef.Name
	secretNamespace := pv.Spec.CSI.NodePublishSecretRef.Namespace
	secret, err := m.SyncController.k8sClient.GetSecret(context.Background(), secretName, secretNamespace)
	if err != nil && k8serrors.IsNotFound(err) {
		return reconcile.Result{}, err
	}
	if _, ok := m.SyncController.discoverys[pv.Name]; !ok {
		if secret.Data[config.PlatformController] != nil && secret.Data[config.CentralBucket] != nil {
			dis, err := task.NewDiscover(m.SyncController.ctx, *secret, m.SyncController.mixture, *pv, m.SyncController.k8sClient)
			if err != nil {
				return reconcile.Result{}, err
			}
			m.SyncController.discoverys[pv.Name] = dis
		}
	} else {
		m.SyncController.discoverys[pv.Name].UpdateDiscovery(*secret)
	}
	return reconcile.Result{}, nil
}

func (c *DataSyncController) SetupWithManager(mgr ctrl.Manager) error {
	controller1, err := controller.New("sync", mgr, controller.Options{Reconciler: &SyncPodManage{
		SyncController: c,
	}})
	if err != nil {
		return err
	}
	controller2, err := controller.New("pv_update", mgr, controller.Options{Reconciler: &PVManage{
		SyncController: c,
	}})
	if err != nil {
		return err
	}

	controller3, err := controller.New("pv_secret", mgr, controller.Options{Reconciler: &SecretManage{
		SyncController: c,
	}})
	if err != nil {
		return err
	}

	err = controller1.Watch(&source.Kind{Type: &corev1.Pod{}}, &handler.EnqueueRequestForObject{}, predicate.Funcs{
		CreateFunc: func(event event.CreateEvent) bool {
			pod := event.Object.(*corev1.Pod)
			klog.V(6).Infof("watch pod %s created", pod.GetName())
			if _, ok := pod.Labels[config.SyncPodLabelKey]; ok {
				return true
			}
			return false
		},
		UpdateFunc: func(updateEvent event.UpdateEvent) bool {
			podNew, ok := updateEvent.ObjectNew.(*corev1.Pod)
			klog.V(6).Infof("watch pod %s updated", podNew.GetName())
			if !ok {
				klog.V(6).Infof("pod.onUpdateFunc Skip object: %v", updateEvent.ObjectNew)
				return false
			}
			if _, ok := podNew.Labels[config.SyncPodLabelKey]; ok {
				return true
			}
			return true
		},
		DeleteFunc: func(deleteEvent event.DeleteEvent) bool {
			pod := deleteEvent.Object.(*corev1.Pod)
			klog.V(6).Infof("watch pod %s deleted", pod.GetName())
			if _, ok := pod.Labels[config.SyncPodLabelKey]; ok {
				return true
			}
			return false
		},
	})
	err = controller2.Watch(&source.Kind{Type: &corev1.PersistentVolume{}}, &handler.EnqueueRequestForObject{}, predicate.Funcs{
		CreateFunc: func(event event.CreateEvent) bool {
			pv, ok := event.Object.(*corev1.PersistentVolume)
			if !ok {
				klog.V(6).Infof("pv.onUpdateFunc Skip object: %v", pv.Name)
				return false
			}
			klog.V(6).Infof("watch pv %s created", pv.GetName())
			// check pv deleted
			if _, ok := pv.Labels[config.SyncPVLabelKey]; ok {
				klog.V(6).Infof("pv %s is sync pv", pv.Name)
				return true
			}
			return false
		},
		UpdateFunc: func(updateEvent event.UpdateEvent) bool {
			pvNew, ok := updateEvent.ObjectNew.(*corev1.PersistentVolume)
			klog.V(6).Infof("watch pv %s updated", pvNew.GetName())
			if !ok {
				klog.V(6).Infof("pod.onUpdateFunc Skip object: %v", updateEvent.ObjectNew)
				return false
			}
			return true
		},
		DeleteFunc: func(deleteEvent event.DeleteEvent) bool {
			pv := deleteEvent.Object.(*corev1.PersistentVolume)
			klog.V(6).Infof("watch pv %s deleted", pv.GetName())
			if _, ok := pv.Labels[config.SyncPVLabelKey]; ok {
				klog.V(6).Infof("pv %s is sync pv", pv.Name)
				return true
			}
			return false
		},
	})

	err = controller3.Watch(&source.Kind{Type: &corev1.Secret{}}, &handler.EnqueueRequestForObject{})
	return err
}

func wrapRegister(mixture string) (prometheus.Registerer, *prometheus.Registry) {
	registry := prometheus.NewRegistry() // replace default so only JuiceFS metrics are exposed
	registerer := prometheus.WrapRegistererWithPrefix("dataset_",
		prometheus.WrapRegistererWith(prometheus.Labels{"mixture": mixture}, registry))
	registerer.MustRegister(collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}))
	registerer.MustRegister(collectors.NewGoCollector())
	return registerer, registry
}

func exposeMetrics(port int, registerer prometheus.Registerer, registry *prometheus.Registry) {
	http.Handle("/metrics", promhttp.HandlerFor(
		registry,
		promhttp.HandlerOpts{
			// Opt into OpenMetrics to support exemplars.
			EnableOpenMetrics: true,
		},
	))
	go func() {
		if err := http.ListenAndServe(fmt.Sprintf(":%d", port), nil); err != nil {
			klog.V(5).Infof("start promthues fail")
		}
	}()
}
