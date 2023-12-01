package sync

import (
	"encoding/json"
	"github.com/juicedata/juicefs-csi-driver/pkg/sync/task"
	"net/http"
	"strings"
)

type CheckSyncStatusParam struct {
	PvName     string   `json:"pv_name"`
	SourcePath []string `json:"source_path"`
}
type CheckSyncStatusRes struct {
	PvName  string             `json:"pv_name"`
	Process []task.ProcessStat `json:"process"`
}

func checkStatus(w http.ResponseWriter, r *http.Request) {
	param := CheckSyncStatusParam{}
	err := json.NewDecoder(r.Body).Decode(&param)
	if err != nil {
		return
	}
	var res CheckSyncStatusRes
	res.PvName = param.PvName
	dis, ok := syncController.discoverys[param.PvName]
	if ok {
		pathsStatus, _ := dis.GetSourcePathsStatus(param.SourcePath)
		process, _ := dis.GetSourceSyncProcess(param.SourcePath...)
		for _, sourcePath := range param.SourcePath {
			var syncStatus int
			if _, ok := pathsStatus[sourcePath]; ok {
				syncStatus = pathsStatus[sourcePath].SyncStatus
			} else {
				syncStatus = task.SYNC_WAIT
			}
			var processRes task.ProcessStat
			if p, ok := process[sourcePath]; ok {
				processRes = p
				processRes.SyncStatus = syncStatus
				processRes.SourcePath = sourcePath
			} else {
				processRes = task.ProcessStat{
					SyncStatus: syncStatus,
					SourcePath: sourcePath,
					Handled:    0,
					Copied:     0,
					Skipped:    0,
					Checked:    0,
					Deleted:    0,
					Failed:     0,
				}
			}
			res.Process = append(res.Process, processRes)
		}
	}
	jstr, _ := json.Marshal(res)
	w.Write(jstr)
	w.WriteHeader(http.StatusOK)
}

func processState(w http.ResponseWriter, r *http.Request) {
	param := task.ProcessStat{}
	err := json.NewDecoder(r.Body).Decode(&param)
	if err != nil {
		return
	}
	if dis, ok := syncController.discoverys[param.PV]; ok {
		param.SourcePath = strings.TrimLeft(param.SourcePath, "/")
		param.SourcePath = strings.TrimRight(param.SourcePath, "/")
		dis.UpdateSourceSyncProcess(param.SourcePath, param)
	}

}
