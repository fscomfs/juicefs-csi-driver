package sync

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/juicedata/juicefs-csi-driver/pkg/config"
	"github.com/juicedata/juicefs-csi-driver/pkg/k8sclient"
	k8s "github.com/juicedata/juicefs-csi-driver/pkg/k8sclient"
	"github.com/juicedata/juicefs-csi-driver/pkg/sync/task"
	"github.com/juicedata/juicefs-csi-driver/pkg/util"
	"github.com/prometheus/client_golang/prometheus"
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
	syncFailure    *prometheus.GaugeVec
	syncSuccess    *prometheus.GaugeVec
	syncDoing      *prometheus.GaugeVec
)

func init() {
	utilruntime.Must(corev1.AddToScheme(scheme))
	utilruntime.Must(clientgoscheme.AddToScheme(scheme))
	syncFailure = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "fail",
		Help: "sync fail",
	}, []string{"pv_name"})
	syncSuccess = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "success",
		Help: "sync success",
	}, []string{"pv_name"})
	syncDoing = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "doing",
		Help: "sync doing",
	}, []string{"pv_name"})
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
		mixture:   config.Mixture,
	}
	return syncController, err
}
func (c *DataSyncController) Run(ctx context.Context) {
	registerer, registry := wrapRegister(c.mixture)
	c.exposeMetrics(config.MetricsPort, registerer, registry)
	go func() {
		//Progress Collector
		startProcessCollector()
	}()
	c.timerInitConfig()
	if err := c.SetupWithManager(c.mgr); err != nil {
		klog.Errorf("Register sync controller error: %v", err)
		return
	}
	if err := c.mgr.Start(ctx); err != nil {
		klog.Errorf("sync manager start error: %v", err)
		return
	}

}
func (c *DataSyncController) timerInitConfig() {
	c.initConfig()
	go func() {
		ticker := time.NewTicker(30 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-c.ctx.Done():
				return
			case <-ticker.C:
				c.initConfig()
			}
		}
	}()
}
func (c *DataSyncController) initConfig() {
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
	for s := range c.discoverys {
		exits := false
		for i := range juicefsPVs.Items {
			if juicefsPVs.Items[i].Name == s && juicefsPVs.Items[i].Annotations[config.SyncPVAnnotationKey] == config.SyncPVLabelVal {
				exits = true
			}
		}
		if !exits {
			c.discoverys[s].Done()
		}
	}
	for i := range juicefsPVs.Items {
		if juicefsPVs.Items[i].Annotations[config.SyncPVAnnotationKey] != config.SyncPVLabelVal {
			continue
		}
		pv := juicefsPVs.Items[i]
		secretName := pv.Spec.CSI.NodePublishSecretRef.Name
		secretNamespace := pv.Spec.CSI.NodePublishSecretRef.Namespace
		secret, err := c.k8sClient.GetSecret(context.Background(), secretName, secretNamespace)
		if err != nil {
			klog.Errorf("sync manager start error: %v", err)
			continue
		}
		if _, ok := c.discoverys[pv.Name]; !ok {
			dis, err := task.NewDiscover(c.ctx, *secret, c.mixture, pv, c.k8sClient)
			if err != nil {
				continue
			}
			c.discoverys[pv.Name] = dis
		} else {
			c.discoverys[pv.Name].UpdateDiscovery(*secret)
			c.discoverys[pv.Name].Start()
		}
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
	if pvName, ok := pod.Labels[config.SyncLabelPVKey]; ok {
		if pvDiscovery, ok := m.SyncController.discoverys[pvName]; ok {
			sourcePath := pod.Annotations[config.SyncPodAnnotationSourcePath]
			if util.IsPodRunning(pod) {
				pvDiscovery.UpdateSourceSyncStatus(sourcePath, task.SYNC_DOING)
				klog.V(5).Infof("sync source path %s syncing", sourcePath)
				return reconcile.Result{}, nil
			}
			if util.IsPodCompleted(pod) {
				pvDiscovery.UpdateSourceSyncStatus(sourcePath, task.SYNC_COMPLETED)
				klog.V(5).Infof("sync source path %s completed", sourcePath)
				return reconcile.Result{}, nil
			}
			if util.IsPodError(pod) || util.IsPodResourceError(pod) {
				pvDiscovery.UpdateSourceSyncStatus(sourcePath, task.SYNC_FAIL)
				klog.V(5).Infof("sync source path %s fail reason:%s", sourcePath, util.PodErrorMessage(pod))
				return reconcile.Result{}, nil
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
		m.SyncController.discoverys[pv.Name].Start()
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
	return err
}

func wrapRegister(mixture string) (prometheus.Registerer, *prometheus.Registry) {
	registry := prometheus.NewRegistry()
	registerer := prometheus.WrapRegistererWithPrefix("sync_",
		prometheus.WrapRegistererWith(prometheus.Labels{"mixture": mixture}, registry))
	//registerer.MustRegister(collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}))
	//registerer.MustRegister(collectors.NewGoCollector())
	return registerer, registry
}

func (c *DataSyncController) exposeMetrics(port int, registerer prometheus.Registerer, registry *prometheus.Registry) {
	registerer.MustRegister(syncFailure)
	registerer.MustRegister(syncSuccess)
	registerer.MustRegister(syncDoing)
	klog.V(6).Infof("register syncFailure syncSuccess  syncDoing metrics")
	go func() {
		http.Handle("/metrics", promhttp.HandlerFor(
			registry,
			promhttp.HandlerOpts{
				// Opt into OpenMetrics to support exemplars.
				EnableOpenMetrics: true,
			},
		))
		if err := http.ListenAndServe(fmt.Sprintf(":%d", port), nil); err != nil {
			klog.V(5).Infof("start promthues exporter fail")
		}
	}()
	go func() {
		for {
			for s := range c.discoverys {
				res := c.discoverys[s].MetaClient.HGetAll(context.Background(), c.discoverys[s].MetaKeyAll())
				mapres, err := res.Result()
				success := 0
				failure := 0
				doing := 0
				if err == nil {
					for _, v := range mapres {
						var res task.SyncProcessStatus
						err = json.Unmarshal([]byte(v), &res)
						if err == nil {
							if res.SyncStatus == task.SYNC_FAIL {
								failure += 1
							}
							if res.SyncStatus == task.SYNC_COMPLETED {
								success += 1
							}
							if res.SyncStatus == task.SYNC_DOING {
								doing += 1
							}
						}
					}
				}
				syncSuccess.WithLabelValues(s).Set(float64(success))
				syncFailure.WithLabelValues(s).Set(float64(failure))
				syncDoing.WithLabelValues(s).Set(float64(doing))
			}
			time.Sleep(5 * time.Second)
		}

	}()
}
