package sync

import (
	"encoding/json"
	"github.com/juicedata/juicefs-csi-driver/pkg/sync/task"
	"net/http"
)

type CheckSyncStatusParam struct {
	PvName     string   `json:"pv_name"`
	SourcePath []string `json:"source_path"`
}
type CheckSyncStatusRes struct {
	PvName  string                   `json:"pv_name"`
	Process []task.SyncProcessStatus `json:"process"`
}

func checkStatus(w http.ResponseWriter, r *http.Request) {
	param := CheckSyncStatusParam{}
	err := json.NewDecoder(r.Body).Decode(&param)
	if err != nil {

	}
	var res CheckSyncStatusRes
	res.PvName = param.PvName
	dis, ok := syncController.discoverys[param.PvName]
	var pathsStatus map[string]task.SyncProcessStatus
	if ok {
		pathsStatus, _ = dis.GetSourcePathsStatus(param.SourcePath)
	}
	for i := range param.SourcePath {
		ss := param.SourcePath[i]
		if sourceStatus, ok := pathsStatus[ss]; ok {
			res.Process = append(res.Process, sourceStatus)
		} else {
			res.Process = append(res.Process, task.SyncProcessStatus{
				SourcePath: ss,
				SyncStatus: task.SYNC_WAIT,
				Current:    0,
				Total:      100,
			})
		}
	}
	jstr, _ := json.Marshal(res)
	w.WriteHeader(http.StatusOK)
	w.Write(jstr)
}
