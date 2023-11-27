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

func CheckSyncStatus(w http.ResponseWriter, r *http.Request) {
	param := CheckSyncStatusParam{}
	err := json.NewDecoder(r.Body).Decode(&param)

	if err != nil {

	}

}
