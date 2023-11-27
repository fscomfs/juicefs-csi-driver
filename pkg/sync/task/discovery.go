package task

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"github.com/juicedata/juicefs-csi-driver/pkg/config"
	"github.com/juicedata/juicefs-csi-driver/pkg/juicefs"
	k8s "github.com/juicedata/juicefs-csi-driver/pkg/k8sclient"
	"github.com/juicedata/juicefs-csi-driver/pkg/meta"
	"github.com/juicedata/juicefs-csi-driver/pkg/util"
	"github.com/redis/go-redis/v9"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/klog"
	"net/http"
	"sync"
	"time"
)

const (
	API_DISCOVERY     = "/discovery"
	API_STATUS_UPDATE = "/update"
	API_STATUS_DELETE = "/delete"

	TASK_CONCURRENT_NUMBER = "SYNC_TASK_CONCURRENT_NUMBER"

	SYNC_WAIT      = 0
	SYNC_DOING     = 1
	SYNC_FAIL      = 2
	SYNC_COMPLETED = 3
	SYNC_NO_NEED   = 4
)

type Discovery struct {
	URL                string
	Mixture            string
	MetaUrl            string
	MetaAccessKey      string
	MetaAccessPassword string
	Storage            string
	BucketUrl          string
	BucketAccessKey    string
	BucketSecretKey    string
	client             *http.Client
	MetaClient         redis.UniversalClient
	Mutex              *sync.Mutex
	K8sClient          *k8s.K8sClient
	Secret             corev1.Secret
	PV                 corev1.PersistentVolume
	Options            []string
	Juicefs            juicefs.Interface
	cancel             context.CancelFunc
}

type SyncProcessStatus struct {
	SourcePath      string    `json:"source_path"`
	SyncStatus      int       `json:"sync_status"` //0 wait sync //1 sync ing //2 sync fail //3 sync completed //4 sync no need
	Total           int64     `json:"total"`       //sync process 0.1
	Current         int64     `json:"current"`
	LastSyncTimeTmp time.Time `json:"last_sync_time_tmp,omitempty"`
	LastSyncTime    time.Time `json:"last_sync_time,omitempty"`
	LastUsageTime   time.Time `json:"last_use_time,omitempty"`
	UsageFrequency  int64     `json:"usage_frequency"`
	Size            int64     `json:"size"`
	TaskName        string    `json:"task_name"`
	LastModifyTime  time.Time `json:"last_modify_time,omitempty"`
	TryTime         int       `json:"try_time"`
}

type DataItem struct {
	SourcePath     string    `json:"source_path,omitempty"`
	TargetPV       string    `json:"target_pv"`
	LastModifyTime time.Time `json:"last_modify_time,omitempty"`
	SystemTime     time.Time `json:"system_time,omitempty"`
}
type PlatformResponse struct {
	Code    int        `json:"code"`
	Data    []DataItem `json:"data"`
	Message string     `json:"message"`
}

type DiscoveryParam struct {
	Mixture string `json:"mixture"`
}

func (d *Discovery) Done() {
	if d.cancel != nil {
		d.cancel()
	}
}
func NewDiscover(ctx context.Context, secret corev1.Secret, mixture string, pv corev1.PersistentVolume, k8sClient *k8s.K8sClient) (*Discovery, error) {
	res := &Discovery{
		Mixture:         mixture,
		URL:             string(secret.Data[config.PlatformController]),
		MetaUrl:         string(secret.Data[config.Metaurl]),
		BucketUrl:       string(secret.Data[config.CentralBucket]),
		BucketAccessKey: string(secret.Data[config.CentralAccessKey]),
		BucketSecretKey: string(secret.Data[config.CentralSecretKey]),
		PV:              pv,
		Secret:          secret,
		Mutex:           new(sync.Mutex),
		K8sClient:       k8sClient,
		MetaClient:      nil,
		Options:         pv.Spec.MountOptions,
		Juicefs:         juicefs.NewJfsProvider(nil, k8sClient),
	}
	err := res.Reinitialize()
	klog.Errorf("init discover sroucePath:%v", res.PV.Name)
	if err != nil {
		klog.Errorf("init discover error:%s", err)
		return nil, err
	}
	cancelCtx, cancelFun := context.WithCancel(ctx)
	res.cancel = cancelFun
	res.PollAndStore(cancelCtx)
	res.DoSync(cancelCtx)
	res.DoCheckSync(cancelCtx)
	return res, nil
}
func (d *Discovery) UpdateDiscovery(secret corev1.Secret) {
	d.URL = string(secret.Data[config.PlatformController])
	d.BucketUrl = string(secret.Data[config.CentralBucket])
	d.BucketUrl = string(secret.Data[config.CentralBucket])
	d.BucketSecretKey = string(secret.Data[config.CentralSecretKey])
	d.BucketAccessKey = string(secret.Data[config.CentralAccessKey])
	if d.MetaUrl != string(secret.Data[config.Metaurl]) {
		d.MetaUrl = string(secret.Data[config.Metaurl])
		d.Reinitialize()
	}
}
func (d *Discovery) Reinitialize() error {
	redisClient, err := meta.NewClient(d.MetaUrl)
	if err != nil {
		klog.Errorf("new Redis client error:%v", err)
		return err
	}
	klog.V(5).Infof("[Discovery] Reinitialize pv:%v", d.PV.Name)
	d.MetaClient = redisClient
	return nil
}
func (d *Discovery) PollAndStore(ctx context.Context) {
	go func() {
		ticker := time.NewTicker(20 * time.Second)
		for {
			select {
			case <-ctx.Done():
				klog.V(5).Infof("[PollAndStore] timer stop pv: %v", d.PV.Name)
				ticker.Stop()
				return
			case <-ticker.C:
				klog.V(5).Infof("[PollAndStore] ticker")
				d.discoverFromPlatform(ctx)
			}
		}
	}()
}

func (d *Discovery) DoSync(ctx context.Context) {
	//sync data set
	go func() {
		ticker := time.NewTicker(10 * time.Second)
		for {
			select {
			case <-ctx.Done():
				ticker.Stop()
				klog.V(5).Infof("[DoSync] timer stop pv: %v", d.PV.Name)
				return
			case <-ticker.C:
				klog.V(5).Infof("[DoSync] ticker")
				d.timeSync()
			}
		}
	}()
}
func (d *Discovery) timeSync() {
	//check sync task concurrent number
	currentNumber, err := d.MetaClient.Get(context.Background(), TASK_CONCURRENT_NUMBER).Int()
	klog.V(6).Infof("[timeSync] time sync currentNumber:%v,config:%v,err:%v", currentNumber, config.SyncConcurrentNumber, err)
	if err != nil && err == redis.Nil {
		if err := d.MetaClient.Set(context.Background(), TASK_CONCURRENT_NUMBER, 0, -1).Err(); err != nil {
			klog.Errorf("[timeSync] error:%v", err)
			return
		}
	}
	if currentNumber >= config.SyncConcurrentNumber {
		time.Sleep(5 * time.Second)
		return
	}
	waitSyncs, err := d.MetaClient.HGetAll(context.Background(), d.metaKeySync()).Result()
	var status SyncProcessStatus
	doSync := false
	klog.V(6).Infof("[timeSync] wait sync source path:%v", waitSyncs)
	if len(waitSyncs) > 0 {
		for s := range waitSyncs {
			if err := json.Unmarshal([]byte(waitSyncs[s]), &status); err != nil {
				klog.Errorf("[timeSync] Unmarshal:%v", err)
				continue
			} else if status.SyncStatus == SYNC_WAIT {
				doSync = true
				break
			}
		}

	}
	if !doSync {
		return
	}
	klog.V(5).Infof("[timeSync] do sync source path:%v,create pod", status.SourcePath)
	//create pod
	err = d.doSync(status)
	if err == nil {
		status.SyncStatus = SYNC_DOING
		status.LastSyncTimeTmp = time.Now()
		d.AddOrUpdate(status)
	} else {
		klog.Errorf("do sync error:%v", err)
	}
}
func (d *Discovery) DoCheckSync(ctx context.Context) {
	go func() {
		ticker := time.NewTicker(10 * time.Second)
		for {
			select {
			case <-ctx.Done():
				klog.V(5).Infof("[DoCheckSync] timer stop pv:%v", d.PV.Name)
				ticker.Stop()
				return
			case <-ticker.C:
				d.timeCheck(ctx)
			}
		}
	}()

}

func (d *Discovery) timeCheck(ctx context.Context) {
	syncIng, err := d.MetaClient.HGetAll(context.Background(), d.metaKeySync()).Result()
	if err != nil {
		return
	}

	if len(syncIng) > 0 {
		for s := range syncIng {
			var status SyncProcessStatus
			if err := json.Unmarshal([]byte(syncIng[s]), &status); err != nil {
				continue
			} else {
				err = d.doSyncCheck(ctx, status)
			}
		}

	}
}

func (d *Discovery) GetSourcePathStatus(sourcePath string) (status SyncProcessStatus, err error) {
	s, err := d.MetaClient.HGet(context.Background(), d.metaKeyAll(), sourcePath).Result()
	if err != nil && err != redis.Nil {
		return SyncProcessStatus{}, err
	}
	err = json.Unmarshal([]byte(s), status)
	return status, err
}

func (d *Discovery) GetSourcePathsStatus(sourcePaths []string) (status map[string]SyncProcessStatus, err error) {
	s, err := d.MetaClient.HMGet(context.Background(), d.metaKeyAll(), sourcePaths...).Result()
	if err != nil && err != redis.Nil {
		return status, err
	}
	for i := range s {
		var ss SyncProcessStatus
		err = json.Unmarshal([]byte(s[i].(string)), &ss)
		status[ss.SourcePath] = ss
	}
	return status, err
}

func (d *Discovery) UpdateSourceSyncStatus(sourcePath string, status int) error {
	syncProcess, err := d.GetSourcePathStatus(sourcePath)
	if err != nil {
		return err
	}
	if status == SYNC_FAIL {
		if syncProcess.TryTime < 3 {
			syncProcess.TryTime++
			syncProcess.SyncStatus = SYNC_WAIT
		} else {
			syncProcess.SyncStatus = SYNC_FAIL
		}
	}
	if status == SYNC_COMPLETED {
		syncProcess.SyncStatus = SYNC_COMPLETED
		syncProcess.LastSyncTime = syncProcess.LastSyncTimeTmp
		if syncProcess.LastSyncTime.Compare(syncProcess.LastModifyTime) > 0 {
			syncProcess.SyncStatus = SYNC_COMPLETED
		} else {
			syncProcess.SyncStatus = SYNC_WAIT
			syncProcess.TryTime = 0
		}
	}
	statusStr, _ := json.Marshal(syncProcess)
	if syncProcess.needSyncQueue() {
		d.MetaClient.HSet(context.Background(), d.metaKeySync(), syncProcess.SourcePath, statusStr)
	} else {
		d.MetaClient.HDel(context.Background(), d.metaKeySync(), syncProcess.SourcePath)
	}
	d.MetaClient.HSet(context.Background(), d.metaKeyAll(), syncProcess.SourcePath, statusStr)
	return nil
}

func (d *Discovery) AddOrUpdate(status SyncProcessStatus) error {
	statusStr, _ := json.Marshal(status)
	if status.LastSyncTime.Compare(status.LastModifyTime) <= 0 {
		err := d.MetaClient.HSet(context.Background(), d.metaKeySync(), status.SourcePath, string(statusStr)).Err()
		if err != nil {
			klog.Errorf("set sync status error:%v", err)
		}
	}
	err := d.MetaClient.HSet(context.Background(), d.metaKeyAll(), status.SourcePath, string(statusStr)).Err()
	return err
}

func (d *Discovery) doSyncCheck(ctx context.Context, syncStatus SyncProcessStatus) error {
	d.Mutex.Lock()
	defer d.Mutex.Unlock()
	v, err := d.GetSourcePathStatus(syncStatus.SourcePath)
	if err == nil {
		syncStatus = v
	}
	if syncStatus.SyncStatus == SYNC_FAIL || syncStatus.SyncStatus == SYNC_COMPLETED {
		pod, err := d.K8sClient.GetPod(context.Background(), syncStatus.TaskName, config.Namespace)
		if err != nil {
			if errors.IsNotFound(err) && syncStatus.SyncStatus == SYNC_DOING {
				fmt.Printf("Pod %s in namespace %s does not exist.\n", syncStatus.TaskName, config.Namespace)
				d.UpdateSourceSyncStatus(syncStatus.SourcePath, SYNC_FAIL)
			}
		}
		err = d.K8sClient.DeletePod(ctx, pod)
		if err != nil && !errors.IsNotFound(err) {
			klog.Errorf("delete complete pod %s ,err:%v", pod.Name, err)
			return err
		}
		return d.UpdateSourceSyncStatus(syncStatus.SourcePath, SYNC_COMPLETED)
	}
	return nil
}

func (d *Discovery) doSync(sync SyncProcessStatus) error {
	d.Mutex.Lock()
	defer d.Mutex.Unlock()
	syncBuilder := PlatformDataSync{
		PodName:         sync.TaskName,
		PVName:          d.PV.Name,
		NameSpace:       config.Namespace,
		ControllerURL:   d.buildControllerURL(),
		SourceStorage:   d.Storage,
		SourceBucket:    d.BucketUrl,
		SourceAccessKey: d.BucketAccessKey,
		SourceSecretKey: d.BucketSecretKey,
		MetaUrl:         d.MetaUrl,
		SourcePath:      sync.SourcePath,
	}
	syncPod := syncBuilder.NewSyncPod()
	_, err := d.K8sClient.CreatePod(context.Background(), syncPod)
	if err != nil {
		return err
	}
	return nil
}

func (d *Discovery) metaKeyAll() string {
	return fmt.Sprintf("SYNC_TASK_%s_ALL", d.PV.Name)
}

func (d *Discovery) metaKeySync() string {
	return fmt.Sprintf("SYNC_TASK_%s_SYNCING", d.PV.Name)
}
func (d *Discovery) buildControllerURL() string {
	return "http://sync_controller." + config.Namespace + "cluster.local"
}

func (d *Discovery) discoverFromPlatform(ctx context.Context) error {
	klog.V(6).Infof("[DiscoverFromPlatform] do start")
	param := DiscoveryParam{
		Mixture: d.Mixture,
	}
	p, _ := json.Marshal(param)
	ctxTimeOut, _ := context.WithTimeout(ctx, 3*time.Second)
	req, err := http.NewRequestWithContext(ctxTimeOut, http.MethodPost, d.URL+API_DISCOVERY, bytes.NewBuffer(p))
	if err != nil {
		klog.Errorf("discover platform info error:%v", req)
		return err
	}

	var resData PlatformResponse
	if d.client == nil {
		d.client = http.DefaultClient
	}
	req.Header.Set("Content-Type", "application/json")
	if res, err := d.client.Do(req); err == nil {
		if res.StatusCode == http.StatusOK {
			if err := json.NewDecoder(res.Body).Decode(&resData); err != nil {
				klog.V(5).Infof("[DiscoverFromPlatform] request fail %v", err)
				return err
			}
		} else {
			return err
		}
	} else {
		klog.Errorf("discover platform info error:%v", err)
		return err
	}
	for _, platSourceData := range resData.Data {
		remoteSysTime := platSourceData.SystemTime
		sysTime := time.Now()
		sub := sysTime.Sub(remoteSysTime)
		var status SyncProcessStatus
		if status, err = d.GetSourcePathStatus(platSourceData.SourcePath); err != nil {
			status = SyncProcessStatus{
				TaskName:        "sync-" + d.PV.Name + "-" + util.RandStringRunes(6),
				SourcePath:      platSourceData.SourcePath,
				SyncStatus:      SYNC_WAIT,
				Total:           100,
				Current:         0,
				LastUsageTime:   platSourceData.LastModifyTime.Add(sub),
				LastSyncTimeTmp: platSourceData.LastModifyTime.Add(sub),
				LastModifyTime:  platSourceData.LastModifyTime.Add(sub),
				UsageFrequency:  0,
			}
		} else {
			status.LastModifyTime = platSourceData.LastModifyTime
		}
		klog.V(6).Infof("source path:%v,val:%v", platSourceData.SourcePath, status)
		if status.LastModifyTime.Compare(status.LastSyncTime) >= 0 {
			klog.V(5).Infof("add to sync queue source path %s,last modify time:%v", platSourceData.SourcePath, platSourceData.LastModifyTime)
			d.AddOrUpdate(status)
		}
	}
	return nil
}

func (s *SyncProcessStatus) needSyncQueue() bool {
	if s.SyncStatus == SYNC_WAIT || s.SyncStatus == SYNC_DOING || s.SyncStatus == SYNC_FAIL {
		return true
	} else {
		return false
	}
}
