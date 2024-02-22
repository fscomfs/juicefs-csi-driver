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
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/util/retry"
	"k8s.io/klog"
	"net/http"
	"path"
	"strings"
	"sync"
	"time"
)

var (
	platform_time = time.Now()
	start_flag    = false
)

const (
	API_DISCOVERY     = "/discovery"
	API_STATUS_UPDATE = "/update"
	API_STATUS_DELETE = "/delete"

	TASK_CONCURRENT_NUMBER = "SYNC_TASK_CONCURRENT_NUMBER"
	SYNC_WAIT              = 0
	SYNC_DOING             = 1
	SYNC_FAIL              = 2
	SYNC_COMPLETED         = 3
	SYNC_NO_NEED           = 4
)

type ProcessStat struct {
	Handled      int64  `json:"handled"`
	Copied       int64  `json:"copied"`        // the number of copied files
	CopiedBytes  int64  `json:"copied_bytes"`  // total amount of copied data in bytes
	Checked      int64  `json:"checked"`       // the number of checked files
	CheckedBytes int64  `json:"checked_bytes"` // total amount of checked data in bytes
	Deleted      int64  `json:"deleted"`       // the number of deleted files
	Skipped      int64  `json:"skipped"`       // the number of files skipped
	Failed       int64  `json:"failed"`        // the number of files that fail to copy
	SyncStatus   int    `json:"sync_status"`
	SourcePath   string `json:"source_path"`
	PV           string `json:"pv"`
}
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
	Envs               string
	client             *http.Client
	MetaClient         redis.UniversalClient
	Mutex              *sync.Mutex
	K8sClient          *k8s.K8sClient
	Secret             corev1.Secret
	PV                 corev1.PersistentVolume
	Options            []string
	Juicefs            juicefs.Interface
	ctx                context.Context
	cancel             context.CancelFunc
	started            bool
	SubDir             string
}

type SyncProcessStatus struct {
	SourcePath      string    `json:"source_path"`
	SyncStatus      int       `json:"sync_status"` //0 wait sync //1 sync ing //2 sync fail //3 sync completed //4 sync no need
	LastSyncTimeTmp time.Time `json:"last_sync_time_tmp,omitempty"`
	LastSyncTime    time.Time `json:"last_sync_time,omitempty"`
	LastUsageTime   time.Time `json:"last_use_time,omitempty"`
	UsageFrequency  int64     `json:"usage_frequency"`
	Size            int64     `json:"size"`
	TaskName        string    `json:"task_name"`
	LastModifyTime  time.Time `json:"last_modify_time,omitempty"`
	TryTime         int       `json:"try_time"`
	CallBack        string    `json:"call_back,omitempty"`
}

func init() {
	go func() {
		ticker := time.NewTicker(1 * time.Second)
		for {
			select {
			case <-ticker.C:
				platform_time.Add(time.Second)
			}
		}
	}()
}
func (s *SyncProcessStatus) ResetProcess() {
	s.TryTime = 0
}

type DataItem struct {
	SourcePath     string    `json:"source_path,omitempty"`
	TargetPV       string    `json:"target_pv"`
	LastModifyTime time.Time `json:"last_modify_time,omitempty"`
	SystemTime     time.Time `json:"system_time,omitempty"`
	CallBack       string    `json:"call_back,omitempty"`
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
	d.Mutex.Lock()
	defer d.Mutex.Unlock()
	if d.started {
		if d.cancel != nil {
			d.cancel()
		}
		d.started = false
	}

}

func (d *Discovery) Start() {
	d.Mutex.Lock()
	defer d.Mutex.Unlock()
	if !d.started {
		d.started = true
		cancelCtx, cancelFun := context.WithCancel(d.ctx)
		d.cancel = cancelFun
		d.PollAndStore(cancelCtx)
		d.DoSync(cancelCtx)
		d.DoCheckSync(cancelCtx)
	}
}

func NewDiscover(ctx context.Context, secret corev1.Secret, mixture string, pv corev1.PersistentVolume, k8sClient *k8s.K8sClient) (*Discovery, error) {
	res := &Discovery{
		Mixture:         mixture,
		URL:             string(secret.Data[config.PlatformController]),
		MetaUrl:         string(secret.Data[config.Metaurl]),
		Storage:         string(secret.Data[config.CentralStorage]),
		BucketUrl:       string(secret.Data[config.CentralBucket]),
		BucketAccessKey: string(secret.Data[config.CentralAccessKey]),
		BucketSecretKey: string(secret.Data[config.CentralSecretKey]),
		Envs:            string(secret.Data["envs"]),
		PV:              pv,
		Secret:          secret,
		Mutex:           new(sync.Mutex),
		K8sClient:       k8sClient,
		MetaClient:      nil,
		Options:         pv.Spec.MountOptions,
		Juicefs:         juicefs.NewJfsProvider(nil, k8sClient),
		ctx:             ctx,
		started:         false,
	}

	var subdir string
	for _, o := range pv.Spec.MountOptions {
		pair := strings.Split(o, "=")
		if len(pair) != 2 {
			continue
		}
		if pair[0] == "subdir" {
			subdir = path.Join("/", pair[1])
		}

	}
	res.SubDir = subdir
	err := res.Reinitialize()
	klog.Errorf("init discover pv:%v", res.PV.Name)
	if err != nil {
		klog.Errorf("init discover error:%s", err)
		return nil, err
	}
	res.Start()
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
		ticker := time.NewTicker(10 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				klog.V(5).Infof("[PollAndStore] timer stop pv: %v", d.PV.Name)
				return
			case <-ticker.C:
				globalLockExec(ctx, d.MetaClient, "discoverFromPlatform", d.discoverFromPlatform)
			}
		}
	}()
}

func (d *Discovery) DoSync(ctx context.Context) {
	//sync data set
	go func() {
		ticker := time.NewTicker(10 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				klog.V(5).Infof("[DoSync] timer stop pv: %v", d.PV.Name)
				return
			case <-ticker.C:
				globalLockExec(ctx, d.MetaClient, "timeSync", d.timeSync)
			}
		}
	}()
}
func (d *Discovery) timeSync(ctx context.Context) error {
	d.Mutex.Lock()
	defer d.Mutex.Unlock()
	if !start_flag {
		return nil
	}
	klog.V(5).Infof("[timeSync] pv:%v,do start", d.PV.Name)
	//check sync task concurrent number
	currentNumber, err := d.MetaClient.Get(context.Background(), TASK_CONCURRENT_NUMBER).Int()
	klog.V(5).Infof("[timeSync] time sync currentNumber:%v,config:%v,err:%v", currentNumber, config.SyncConcurrentNumber, err)
	if err != nil && err == redis.Nil {
		if err := d.MetaClient.Set(context.Background(), TASK_CONCURRENT_NUMBER, 0, -1).Err(); err != nil {
			klog.Errorf("[timeSync] error:%v", err)
			return err
		}
	}
	if currentNumber >= config.SyncConcurrentNumber {
		time.Sleep(5 * time.Second)
		return nil
	}
	waitSyncs, err := d.MetaClient.HGetAll(context.Background(), d.metaKeySync()).Result()
	var status SyncProcessStatus
	doSync := false
	if len(waitSyncs) > 0 {
		for s := range waitSyncs {
			if status, err = d.GetSourcePathStatus(s); err == nil {
				if status.SyncStatus == SYNC_WAIT && platform_time.Sub(status.LastSyncTimeTmp) > time.Minute {
					doSync = true
					break
				}
			} else {
				continue
			}
		}
	}
	if !doSync {
		return nil
	}
	klog.V(5).Infof("[timeSync] do sync source path:%v,create pod", status.SourcePath)
	//create pod
	err = d.doSync(status)
	if err == nil {
		d.MetaClient.Incr(context.Background(), TASK_CONCURRENT_NUMBER)
		d.UpdateSourceSyncStatus(status.SourcePath, SYNC_DOING)
	} else {
		klog.Errorf("do sync error:%v", err)
	}
	return err
}
func (d *Discovery) DoCheckSync(ctx context.Context) {
	go func() {
		ticker := time.NewTicker(30 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				klog.V(5).Infof("[DoCheckSync] timer stop pv:%v", d.PV.Name)
				return
			case <-ticker.C:
				globalLockExec(ctx, d.MetaClient, "timerCheck", d.timerCheck)
			}
		}
	}()

}

func (d *Discovery) timerCheck(ctx context.Context) error {
	d.Mutex.Lock()
	defer d.Mutex.Unlock()
	klog.V(5).Infof("[timerCheck] pv:%v,do start", d.PV.Name)
	syncIng, err := d.MetaClient.HGetAll(context.Background(), d.metaKeySync()).Result()
	if err != nil {
		return err
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
	return err
}

func (d *Discovery) GetSourcePathStatus(sourcePath string) (status SyncProcessStatus, err error) {
	s, err := d.MetaClient.HGet(context.Background(), d.MetaKeyAll(), sourcePath).Bytes()
	if err != nil {
		return SyncProcessStatus{}, err
	}
	var res SyncProcessStatus
	err = json.Unmarshal(s, &res)
	return res, nil
}

func (d *Discovery) GetSourcePathsStatus(sourcePaths []string) (status map[string]SyncProcessStatus, err error) {
	s, err := d.MetaClient.HMGet(context.Background(), d.MetaKeyAll(), sourcePaths...).Result()
	if err != nil && err != redis.Nil {
		return status, err
	}
	status = make(map[string]SyncProcessStatus)
	for i := range s {
		var ss SyncProcessStatus
		if s[i] != nil {
			err = json.Unmarshal([]byte(s[i].(string)), &ss)
			if err != nil {
				klog.V(5).Infof("[GetSourcePathsStatus] unmarshal error:%v", err)
				return status, err
			}
			status[ss.SourcePath] = ss
		}

	}
	return status, err
}
func (d *Discovery) UpdateSourceSyncModifyTime(sourcePath string, modifyTime time.Time) error {
	id := util.RandStringRunes(6)
	if !acquireLock(context.Background(), d.MetaClient, sourcePath, id, 3*time.Second, 3*time.Second) {
		return nil
	}
	defer releaseLock(context.Background(), d.MetaClient, sourcePath, id)
	syncProcess, err := d.GetSourcePathStatus(sourcePath)
	if err != nil {
		return err
	}
	if syncProcess.LastModifyTime.Compare(modifyTime) >= 0 {
		return nil
	}
	status := syncProcess.SyncStatus
	syncProcess.LastModifyTime = modifyTime
	if status == SYNC_COMPLETED || status == SYNC_FAIL {
		syncProcess.SyncStatus = SYNC_WAIT
		syncProcess.ResetProcess()
	}
	statusStr, _ := json.Marshal(syncProcess)
	if syncProcess.needSyncQueue() {
		d.MetaClient.HSet(context.Background(), d.metaKeySync(), syncProcess.SourcePath, statusStr)
	} else {
		d.MetaClient.HDel(context.Background(), d.metaKeySync(), syncProcess.SourcePath)
	}
	d.MetaClient.HSet(context.Background(), d.MetaKeyAll(), syncProcess.SourcePath, statusStr)
	return nil
}
func (d *Discovery) UpdateSourceSyncStatus(sourcePath string, status int) error {
	id := util.RandStringRunes(6)
	if !acquireLock(context.Background(), d.MetaClient, sourcePath, id, 3*time.Second, 3*time.Second) {
		return nil
	}
	defer releaseLock(context.Background(), d.MetaClient, sourcePath, id)
	syncProcess, err := d.GetSourcePathStatus(sourcePath)
	if err != nil {
		return err
	}
	klog.V(6).Infof("source path:%s change status:%v,current status: %v", syncProcess.SourcePath, status, syncProcess.SyncStatus)
	if status == SYNC_FAIL || status == SYNC_COMPLETED {
		ctx, _ := context.WithTimeout(context.Background(), 2*time.Second)
		err := d.K8sClient.CoreV1().Pods(config.Namespace).Delete(ctx, syncProcess.TaskName, v1.DeleteOptions{})
		if err != nil && !errors.IsNotFound(err) {
			return err
		}
		if syncProcess.SyncStatus != SYNC_FAIL && syncProcess.SyncStatus != SYNC_COMPLETED && syncProcess.SyncStatus != SYNC_WAIT {
			d.MetaClient.Decr(context.Background(), TASK_CONCURRENT_NUMBER)
		}
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
			syncProcess.ResetProcess()
		}
	}
	if status == SYNC_DOING {
		syncProcess.SyncStatus = SYNC_DOING
		syncProcess.LastSyncTimeTmp = platform_time
		syncProcess.ResetProcess()
	}
	statusStr, _ := json.Marshal(syncProcess)
	if syncProcess.needSyncQueue() {
		d.MetaClient.HSet(context.Background(), d.metaKeySync(), syncProcess.SourcePath, statusStr)
	} else {
		d.MetaClient.HDel(context.Background(), d.metaKeySync(), syncProcess.SourcePath)
	}
	d.MetaClient.HSet(context.Background(), d.MetaKeyAll(), syncProcess.SourcePath, statusStr)
	go func() {
		callbackSyncStatus(syncProcess)
	}()
	return nil
}

func callbackSyncStatus(status SyncProcessStatus) error {
	client := http.DefaultClient
	p, _ := json.Marshal(status)
	retry.OnError(wait.Backoff{
		Duration: time.Second,
		Factor:   1.2,
		Steps:    3,
		Jitter:   0,
		Cap:      0,
	}, func(err error) bool {
		if err == nil {
			return false
		}
		return true
	}, func() error {
		ctxTimeOut, _ := context.WithTimeout(context.Background(), 2*time.Second)
		req, err := http.NewRequestWithContext(ctxTimeOut, http.MethodPost, status.CallBack, bytes.NewBuffer(p))
		if err != nil {
			klog.Errorf("callbackSyncStatus error:%v", err)
			return err
		}
		req.Header.Set("Content-Type", "application/json")
		if res, err := client.Do(req); err == nil {
			if res.StatusCode == http.StatusOK {
				return nil
			} else {
				return err
			}
		} else {
			klog.Errorf("callbackSyncStatus error:%v", err)
			return err
		}
	})
	return nil
}
func callbackSyncTaskProcess(status SyncProcessStatus, stat ProcessStat) error {
	client := http.DefaultClient
	p, _ := json.Marshal(stat)
	ctxTimeOut, _ := context.WithTimeout(context.Background(), 2*time.Second)
	req, err := http.NewRequestWithContext(ctxTimeOut, http.MethodPut, status.CallBack, bytes.NewBuffer(p))
	if err != nil {
		klog.Errorf("callbackSyncStatus error:%v", err)
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	if res, err := client.Do(req); err == nil {
		if res.StatusCode == http.StatusOK {
			return nil
		} else {
			return err
		}
		defer res.Body.Close()
	} else {
		klog.Errorf("callbackSyncStatus error:%v", err)
		return err
	}
	return nil
}

func (d *Discovery) UpdateSourceSyncProcess(sourcePath string, stat ProcessStat) error {
	statusStr, _ := json.Marshal(stat)
	err := d.MetaClient.HSet(context.Background(), d.metaKeyProcess(), sourcePath, statusStr).Err()
	if err == nil {
		status, err := d.GetSourcePathStatus(sourcePath)
		if err == nil && status.CallBack != "" {
			go func() {
				callbackSyncTaskProcess(status, stat)
			}()
		}
	}
	return err
}
func (d *Discovery) GetSourceSyncProcess(sourcePaths ...string) (process map[string]ProcessStat, err error) {
	process = make(map[string]ProcessStat)
	if res, err2 := d.MetaClient.HMGet(context.Background(), d.metaKeyProcess(), sourcePaths...).Result(); err2 == nil {
		for i := range res {
			var state ProcessStat
			if res[i] != nil {
				s := res[i].(string)
				json.Unmarshal([]byte(s), &state)
				process[state.SourcePath] = state
			}
		}
	}
	return process, err
}

func (d *Discovery) AddOrUpdate(status SyncProcessStatus, isAdd bool) error {
	if !isAdd {
		return d.UpdateSourceSyncModifyTime(status.SourcePath, status.LastModifyTime)
	} else {
		statusStr, _ := json.Marshal(status)
		err := d.MetaClient.HSet(context.Background(), d.metaKeySync(), status.SourcePath, string(statusStr)).Err()
		if err != nil {
			klog.Errorf("set sync status error:%v", err)
		}
		err = d.MetaClient.HSet(context.Background(), d.MetaKeyAll(), status.SourcePath, string(statusStr)).Err()
		return err
	}
}

func (d *Discovery) doSyncCheck(ctx context.Context, syncStatus SyncProcessStatus) error {
	v, err := d.GetSourcePathStatus(syncStatus.SourcePath)
	if err == nil {
		syncStatus = v
	} else {
		return nil
	}
	if syncStatus.SyncStatus == SYNC_WAIT {
		return nil
	}
	pod, err := d.K8sClient.GetPod(context.Background(), syncStatus.TaskName, config.Namespace)
	if err != nil {
		if errors.IsNotFound(err) && (syncStatus.SyncStatus == SYNC_DOING || syncStatus.SyncStatus == SYNC_FAIL) {
			klog.V(5).Infof("Pod %s in namespace %s does not exist.\n", syncStatus.TaskName, config.Namespace)
			return d.UpdateSourceSyncStatus(syncStatus.SourcePath, SYNC_FAIL)
		}
	}
	if pod != nil {
		if syncStatus.SyncStatus == SYNC_DOING {
			if util.IsPodError(pod) || util.IsPodResourceError(pod) {
				return d.UpdateSourceSyncStatus(syncStatus.SourcePath, SYNC_FAIL)
			}
			if util.IsPodCompleted(pod) {
				return d.UpdateSourceSyncStatus(syncStatus.SourcePath, SYNC_COMPLETED)
			}
		}
	}
	return nil
}

func (d *Discovery) doSync(sync SyncProcessStatus) error {
	var envs map[string]string
	if d.Envs != "" {
		err := json.Unmarshal([]byte(d.Envs), &envs)
		if err != nil {
			klog.Errorf("envs parse error:%v", err)
			return err
		}
	}
	syncBuilder := PlatformDataSync{
		PodName:         sync.TaskName,
		PVName:          d.PV.Name,
		SecretName:      d.Secret.Name,
		NameSpace:       config.Namespace,
		ControllerURL:   config.ControllerURL,
		SourceStorage:   d.Storage,
		SourceBucket:    d.BucketUrl,
		SubDir:          d.SubDir,
		SourceAccessKey: d.BucketAccessKey,
		SourceSecretKey: d.BucketSecretKey,
		MetaUrl:         d.MetaUrl,
		SourcePath:      sync.SourcePath,
		Envs:            envs,
	}
	syncPod := syncBuilder.NewSyncPod()
	_, err := d.K8sClient.CreatePod(context.Background(), syncPod)
	if err != nil {
		return err
	}
	return nil
}

func (d *Discovery) MetaKeyAll() string {
	return fmt.Sprintf("SYNC_TASK_%s_ALL", d.PV.Name)
}

func (d *Discovery) metaKeyProcess() string {
	return fmt.Sprintf("SYNC_TASK_%s_PROCESS", d.PV.Name)
}

func (d *Discovery) metaKeySync() string {
	return fmt.Sprintf("SYNC_TASK_%s_SYNCING", d.PV.Name)
}

func (d *Discovery) discoverFromPlatform(ctx context.Context) error {
	d.Mutex.Lock()
	defer d.Mutex.Unlock()
	klog.V(5).Infof("[DiscoverFromPlatform] pv:%v,do start", d.PV.Name)
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
		platform_time = platSourceData.SystemTime
		start_flag = true
		var status SyncProcessStatus
		var isAdd = false
		if status, err = d.GetSourcePathStatus(platSourceData.SourcePath); err != nil {
			klog.V(6).Infof("new process status source path:%v,err:%v", platSourceData.SourcePath, err)
			status = SyncProcessStatus{
				TaskName:        "sync-" + d.PV.Name + "-" + util.RandStringRunes(6),
				SourcePath:      platSourceData.SourcePath,
				SyncStatus:      SYNC_WAIT,
				LastUsageTime:   platSourceData.LastModifyTime,
				LastSyncTimeTmp: platSourceData.LastModifyTime.Add(time.Minute),
				LastModifyTime:  platSourceData.LastModifyTime,
				UsageFrequency:  0,
				CallBack:        platSourceData.CallBack,
			}
			isAdd = true
		}
		if platSourceData.LastModifyTime.Compare(status.LastModifyTime) > 0 || isAdd {
			status.LastModifyTime = platSourceData.LastModifyTime
			klog.V(5).Infof("add to sync queue source path %s,last modify time:%v", platSourceData.SourcePath, platSourceData.LastModifyTime)
			d.AddOrUpdate(status, isAdd)
		}
	}
	return nil
}

func (s *SyncProcessStatus) needSyncQueue() bool {
	if s.SyncStatus == SYNC_WAIT || s.SyncStatus == SYNC_DOING {
		return true
	} else {
		if s.SyncStatus == SYNC_FAIL {
			if s.TryTime < 3 {
				return true
			}
		}
		return false
	}
}

func acquireLock(ctx context.Context, client redis.UniversalClient, lockKey, identifier string, lockTimeout time.Duration, waitTime time.Duration) bool {
	key := fmt.Sprintf("SYNC_LOCK_%s", lockKey)
	ok, err := client.SetNX(ctx, key, identifier, lockTimeout).Result()
	if err != nil {
		fmt.Printf("Error acquiring lock: %v\n", err)
		return false
	}
	if waitTime == 0 || ok {
		return ok
	}
	startTime := time.Now()
	for time.Since(startTime) < waitTime {
		_, err := client.BLPop(ctx, 500*time.Millisecond, key).Result()
		if err == redis.Nil {
			ok, err := client.SetNX(ctx, key, identifier, lockTimeout).Result()
			if err != nil {
				return false
			}
			if ok {
				return true
			}
		} else if err != nil {
			return false
		}
	}
	return false
}

func globalLockExec(ctx context.Context, universalClient redis.UniversalClient, lockKey string, cal func(ctx2 context.Context) error) {
	key := fmt.Sprintf("SYNC_GLOBAL_TASK_LOCK_%s", lockKey)
	id := util.RandStringRunes(6)
	if !acquireLock(ctx, universalClient, key, id, time.Minute, 0) {
		return
	}
	defer releaseLock(ctx, universalClient, key, id)
	cal(ctx)
}

func releaseLock(ctx context.Context, client redis.UniversalClient, lockKey, identifier string) bool {
	key := fmt.Sprintf("SYNC_LOCK_%s", lockKey)
	script := `
		if redis.call("get", KEYS[1]) == ARGV[1] then
			return redis.call("del", KEYS[1])
		else
			return 0
		end
	`
	result, err := client.Eval(ctx, script, []string{key}, identifier).Result()
	if err != nil {
		fmt.Printf("Error releasing lock: %s\n", err)
		return false
	}
	return result.(int64) == 1
}
