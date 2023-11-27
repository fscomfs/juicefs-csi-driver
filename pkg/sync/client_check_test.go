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
	"math/rand"
	"net/http"
	"path"
	"time"
)

var ()

type CheckSyncStatus struct {
	PV string `json:"PV"`
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
		klog.V(5).Infof("Error marshaling JSON:", err)
		return
	}
	httpClient := &http.Client{}
	retryCount := 0
	p := mpb.New(
		mpb.WithAutoRefresh(),
	)
	bars := make(map[string]*mpb.Bar)
	for i := range param.SourcePath {
		bars[param.SourcePath[i]] = p.AddBar(100,
			mpb.PrependDecorators(
				decor.Name(path.Ext(param.SourcePath[i]), decor.WC{C: decor.DidentRight | decor.DextraSpace}),
				decor.Name("syncing", decor.WCSyncSpaceR),
				decor.Percentage(decor.WC{W: 5}),
			),
		)
	}
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
			exitFlag := true
			errorMessage := ""
			for i := range checkSyncStatusRes.Process {
				if bar, ok := bars[checkSyncStatusRes.Process[i].SourcePath]; ok {
					if checkSyncStatusRes.Process[i].SyncStatus == task.SYNC_COMPLETED || checkSyncStatusRes.Process[i].SyncStatus == task.SYNC_NO_NEED {
						bar.SetCurrent(checkSyncStatusRes.Process[i].Current)
						bar.SetTotal(checkSyncStatusRes.Process[i].Total, true)
						bar.Abort(false)
						bar.SetRefill(0)
						bar.TraverseDecorators(func(decorator decor.Decorator) {
							decor.Name("done")
						})
					} else if checkSyncStatusRes.Process[i].SyncStatus == task.SYNC_DOING {
						bar.SetTotal(checkSyncStatusRes.Process[i].Total, true)
						bar.SetCurrent(checkSyncStatusRes.Process[i].Current)
						exitFlag = false
					} else if checkSyncStatusRes.Process[i].SyncStatus == task.SYNC_FAIL {
						bar.SetTotal(checkSyncStatusRes.Process[i].Total, true)
						bar.SetCurrent(checkSyncStatusRes.Process[i].Current)
						bar.Abort(false)
						bar.SetRefill(0)
						bar.TraverseDecorators(func(decorator decor.Decorator) {
							decor.Name("sync fail")
						})
						errorMessage += fmt.Sprint("%s sync fail\n", checkSyncStatusRes.Process[i].SourcePath)
					}
				}
			}
			if exitFlag {
				if errorMessage != "errorMessage" {
					klog.V(5).Infof(errorMessage)
				}
				klog.V(5).Infof("sync check done")
				return
			}
		}
		time.Sleep(1 * time.Second)
	}
}

func main() {
	numBars := 4
	// to support color in Windows following both options are required
	p := mpb.New(
		mpb.WithAutoRefresh(),
	)
	for i := 0; i < numBars; i++ {
		task := fmt.Sprintf("Task#%02d:", i)
		queue := make([]*mpb.Bar, 2)
		queue[0] = p.AddBar(rand.Int63n(201)+100,
			mpb.PrependDecorators(
				decor.Name(task, decor.WC{C: decor.DidentRight | decor.DextraSpace}),
				decor.Name("downloading", decor.WCSyncSpaceR),
				decor.CountersNoUnit("%d / %d", decor.WCSyncWidth),
			),
			mpb.AppendDecorators(
				decor.OnComplete(decor.Percentage(decor.WC{W: 5}), "done"),
			),
		)
		queue[1] = p.AddBar(rand.Int63n(101)+100,
			mpb.BarQueueAfter(queue[0]), // this bar is queued
			mpb.BarFillerClearOnComplete(),
			mpb.PrependDecorators(
				decor.Name(task, decor.WC{C: decor.DidentRight | decor.DextraSpace}),
				decor.OnComplete(decor.EwmaETA(decor.ET_STYLE_MMSS, 0, decor.WCSyncWidth), ""),
			),
			mpb.AppendDecorators(
				decor.OnComplete(decor.Percentage(decor.WC{W: 5}), ""),
			),
		)

		go func() {
			for _, b := range queue {
				complete(b)
			}
		}()
	}

	p.Wait()

}

func complete(bar *mpb.Bar) {
	max := 100 * time.Millisecond
	for !bar.Completed() {
		// start variable is solely for EWMA calculation
		// EWMA's unit of measure is an iteration's duration
		start := time.Now()
		time.Sleep(time.Duration(rand.Intn(10)+1) * max / 10)
		// we need to call EwmaIncrement to fulfill ewma decorator's contract
		bar.EwmaIncrInt64(rand.Int63n(5)+1, time.Since(start))
	}
}
