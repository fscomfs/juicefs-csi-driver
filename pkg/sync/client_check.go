package sync

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"github.com/juicedata/juicefs-csi-driver/pkg/config"
	"github.com/juicedata/juicefs-csi-driver/pkg/sync/task"
	"github.com/vbauerster/mpb/v7"
	"github.com/vbauerster/mpb/v7/decor"
	"k8s.io/klog"
	"log"
	"net/http"
	"strings"
	"sync"
	"time"
)

var ()

type CheckSyncStatus struct {
	PV string `json:"PV"`
}
type title struct {
	decor.Decorator
	name         string
	statusChan   <-chan task.ProcessStat
	status       int
	msg          string
	current      int64
	Handled      int64
	Copied       int64 // the number of copied files
	CopiedBytes  int64 // total amount of copied data in bytes
	Checked      int64 // the number of checked files
	CheckedBytes int64 // total amount of checked data in bytes
	Deleted      int64 // the number of deleted files
	Skipped      int64 // the number of files skipped
	Failed       int64 // the number of files that fail to copy
	SyncStatus   int
}

func (d *title) Decor(stat decor.Statistics) string {
	select {
	case status := <-d.statusChan:
		if status.SyncStatus == 0 {
			d.msg = "Wait for synchronous task execution"
		}
		d.status = status.SyncStatus
		d.Handled = status.Handled
		d.Copied = status.Copied
		d.Skipped = status.Skipped
		d.Deleted = status.Deleted
		d.Checked = status.Checked
		d.CopiedBytes = status.CopiedBytes
		if status.SyncStatus == 1 {
			d.msg = fmt.Sprintf("Scanned:%d,Copied:%d,Skipped:%d,Failed:%d ", d.Handled, d.Copied, d.Skipped, d.Failed)
		}
		if status.SyncStatus == 2 {
			d.msg = "Sync failed "
		}
		if status.SyncStatus == 3 {
			d.msg = "Sync completed "
		}
		if status.SyncStatus == 4 {
			d.msg = "No sync needed "
		}
		break
	default:
		break
	}
	return fmt.Sprintf("%s %s", d.name, d.msg)
}
func (d *SyncProcessDecord) Decor(stat decor.Statistics) string {
	select {
	case status := <-d.statusChan:
		d.status = status
		d.msg = status.message
		break
	default:
		break
	}
	return fmt.Sprintf("%s", d.msg)
}

type SyncProcessDecord struct {
	decor.Decorator
	time       string
	statusChan <-chan SyncMessage
	status     SyncMessage
	msg        string
}
type SyncMessage struct {
	status  int
	message string
}

func newTitleDecorator(name string, statusChan <-chan task.ProcessStat) decor.Decorator {
	return &title{
		Decorator:  decor.Name(name),
		name:       name,
		statusChan: statusChan,
		status:     0,
	}
}

func newProcessDecorator(name string, statusChan <-chan SyncMessage) decor.Decorator {
	return &SyncProcessDecord{
		Decorator:  decor.Name(name),
		statusChan: statusChan,
		status: SyncMessage{
			status:  1,
			message: "",
		},
	}
}

func (c *CheckSyncStatus) Run(ctx context.Context) {
	klog.V(5).Infof("check sync status")
	if config.DstPV == "" {
		log.Println("target PV is empty")
		return
	}
	for i := range config.SourcePath {
		config.SourcePath[i] = strings.TrimRight(config.SourcePath[i], "/")
	}
	param := CheckSyncStatusParam{
		PvName:     config.DstPV,
		SourcePath: config.SourcePath,
	}
	postData, err := json.Marshal(param)
	if err != nil {
		log.Printf("Error marshaling JSON:%v", err)
		return
	}
	httpClient := &http.Client{}
	retryCount := 0
	var wg sync.WaitGroup
	wg.Add(1)
	// passed wg will be accounted at p.Wait() call
	p := mpb.New(mpb.WithWaitGroup(&wg), mpb.WithWidth(100))
	processMessage := make(chan SyncMessage)
	decors := []decor.Decorator{
		newProcessDecorator("", processMessage),
	}
	p.AddSpinner(0,
		mpb.BarFillerClearOnComplete(),
		mpb.PrependDecorators(decors...),
	)
	bars := make(map[string]*mpb.Bar)
	status := make(map[string]chan task.ProcessStat)
	for i := range param.SourcePath {
		s := make(chan task.ProcessStat, 1)
		status[param.SourcePath[i]] = s
		title := ""
		if len(param.SourcePath[i]) > 10 {
			title = param.SourcePath[i][len(param.SourcePath[i])-10:]
		} else {
			title = param.SourcePath[i]
		}
		decors := []decor.Decorator{
			newTitleDecorator(fmt.Sprintf("%v:", title), s),
			decor.Merge(decor.CurrentNoUnit("total: %d", decor.WCSyncSpaceR), decor.WCSyncSpaceR),
		}
		bars[param.SourcePath[i]] = p.Add(0, newSpinner(),
			mpb.BarFillerClearOnComplete(),
			mpb.PrependDecorators(decors...),
		)
	}

	startTime := time.Now()
	go func() {
		defer wg.Done()
		for {
			select {
			case <-ctx.Done():
				return
			default:
				req, err := http.NewRequestWithContext(ctx, http.MethodPost, config.ControllerURL+config.CheckSyncStatusApi, bytes.NewBuffer(postData))
				req.Header.Set("Content-Type", "application/json")
				if err != nil {
					return
				}
				t := ""
				if time.Since(startTime).Seconds() >= 60 {
					t = fmt.Sprintf("%d min", int(time.Since(startTime).Minutes()))
				} else {
					t = fmt.Sprintf("%d s", int(time.Since(startTime).Seconds()))
				}
				message := SyncMessage{
					status:  1,
					message: fmt.Sprintf("Sync check %s waiting", t),
				}

				res, err := httpClient.Do(req)
				if err != nil && retryCount < 10 {
					retryCount++
					message.status = 2
					if retryCount >= 10 {
						message.message = fmt.Sprintf("Sync check %s end for error:%v", t, err.Error())
						processMessage <- message
						return
					} else {
						message.message = fmt.Sprintf("Sync check %s retry %d,error:%v", t, retryCount, err.Error())
						processMessage <- message
					}
					time.Sleep(1 * time.Second)
					continue
				}
				retryCount = 0
				var checkSyncStatusRes CheckSyncStatusRes
				if res.StatusCode == http.StatusOK {
					processMessage <- message
					retryCount = 0
					json.NewDecoder(res.Body).Decode(&checkSyncStatusRes)
					exit := true
					for i := range checkSyncStatusRes.Process {
						s := status[checkSyncStatusRes.Process[i].SourcePath]
						if bar, ok := bars[checkSyncStatusRes.Process[i].SourcePath]; ok {
							bar.SetCurrent(checkSyncStatusRes.Process[i].Handled)
							s <- checkSyncStatusRes.Process[i]
							if checkSyncStatusRes.Process[i].SyncStatus == task.SYNC_WAIT || checkSyncStatusRes.Process[i].SyncStatus == task.SYNC_DOING ||
								checkSyncStatusRes.Process[i].SyncStatus == task.SYNC_FAIL {
								exit = false
							}
						}
					}
					if exit {
						return
					}
				}
				time.Sleep(1 * time.Second)
			}
		}
	}()
	wg.Wait()
}

func newSpinner() mpb.BarFiller {
	spinnerStyle := []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}
	//for i, s := range spinnerStyle {
	//	spinnerStyle[i] = "\033[1;32m" + s + "\033[0m"
	//}
	return mpb.NewBarFiller(mpb.SpinnerStyle(spinnerStyle...))
}
