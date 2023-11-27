package sync

import (
	"bytes"
	"encoding/json"
	"fmt"
	"github.com/juicedata/juicefs-csi-driver/pkg/config"
	"github.com/juicedata/juicefs-csi-driver/pkg/sync/task"
	"github.com/vbauerster/mpb/v8"
	"github.com/vbauerster/mpb/v8/decor"
	"k8s.io/klog"
	"math"
	"net/http"
	"path"
	"time"
)

var ()

type CheckSyncStatus struct {
	PV string `json:"PV"`
}
type title struct {
	decor.Decorator
	name       string
	statusChan <-chan int
	status     int
	msg        string
	count      int
	limit      int
}

func (d *title) Decor(stat decor.Statistics) (string, int) {
	select {
	case status := <-d.statusChan:
		d.count = d.limit
		if status == 0 {
			d.msg = "Wait for synchronous task execution"
		}
		if status == 1 {
			d.msg = "Synchronous task is currently executing"
		}
		if status == 2 {
			d.msg = "Synchronization failed"
		}
		if status == 3 {
			d.msg = "Sync completed"
		}
		if status == 4 {
			d.msg = "No synchronization needed"
		}
		d.status = status
		break
	default:
		break
	}
	if d.status == 1 {
		return d.Decorator.Decor(stat)
	}
	return fmt.Sprintf("%s %s", d.name, d.msg), math.MaxInt
}

func newTitleDecorator(name string, statusChan <-chan int, limit int) decor.Decorator {
	return &title{
		Decorator:  decor.Name(name),
		name:       name,
		statusChan: statusChan,
		status:     0,
		limit:      limit,
	}
}

func (c *CheckSyncStatus) Run() {
	klog.V(5).Infof("check sync status")
	if config.DstPV == "" {
		klog.V(5).Infof("target PV is empty")
		return
	}
	param := CheckSyncStatusParam{
		PvName:     config.DstPV,
		SourcePath: []string{},
	}
	postData, err := json.Marshal(param)
	if err != nil {
		klog.V(5).Infof("Error marshaling JSON:%v", err)
		return
	}
	httpClient := &http.Client{}
	retryCount := 0
	p := mpb.New(
		mpb.WithWidth(80),
	)
	bars := make(map[string]*mpb.Bar)
	status := make(map[string]chan int)
	for i := range param.SourcePath {
		s := make(chan int, 1)
		status[param.SourcePath[i]] = s
		bars[param.SourcePath[i]] = p.AddBar(0,
			mpb.BarFillerClearOnComplete(),
			mpb.BarFillerTrim(),
			mpb.PrependDecorators(newTitleDecorator(fmt.Sprintf("%v:", path.Base(param.SourcePath[i])), s, 16)),
			mpb.AppendDecorators(decor.Any(func(statistics decor.Statistics) string {
				return fmt.Sprintf("(%d/%d)", statistics.Current, statistics.Total)
			}, decor.WCSyncWidth)),
		)
	}
	go func() {
		for {
			req, err := http.NewRequest(http.MethodPost, config.ControllerURL+config.CheckSyncStatusAPi, bytes.NewBuffer(postData))
			if err != nil {
				return
			}
			res, err := httpClient.Do(req)
			if err != nil && retryCount < 10 {
				retryCount++
				klog.V(5).Infof("[CheckSyncStatus]sync request fail err;%v syncInfo:%v", err, param)
				if retryCount >= 10 {
					return
				}
				time.Sleep(1 * time.Second)
				continue
			}
			retryCount = 0
			var checkSyncStatusRes CheckSyncStatusRes
			if res.StatusCode == http.StatusOK {
				json.NewDecoder(res.Body).Decode(&checkSyncStatusRes)
				for i := range checkSyncStatusRes.Process {
					s := status[checkSyncStatusRes.Process[i].SourcePath]
					if bar, ok := bars[checkSyncStatusRes.Process[i].SourcePath]; ok {
						s <- checkSyncStatusRes.Process[i].SyncStatus
						if checkSyncStatusRes.Process[i].SyncStatus == task.SYNC_WAIT {
							bar.SetTotal(1, false)
						} else if checkSyncStatusRes.Process[i].SyncStatus == task.SYNC_COMPLETED || checkSyncStatusRes.Process[i].SyncStatus == task.SYNC_NO_NEED {
							bar.SetTotal(-1, true)
							bar.Abort(false)
						} else if checkSyncStatusRes.Process[i].SyncStatus == task.SYNC_DOING {
							bar.SetTotal(checkSyncStatusRes.Process[i].Total, false)
							bar.SetCurrent(checkSyncStatusRes.Process[i].Current)
						} else if checkSyncStatusRes.Process[i].SyncStatus == task.SYNC_FAIL {

						}
					}
				}
			}
			time.Sleep(1 * time.Second)
		}
	}()
	p.Wait()
}
