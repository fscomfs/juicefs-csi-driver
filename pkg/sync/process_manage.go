package sync

import (
	"fmt"
	"github.com/juicedata/juicefs-csi-driver/pkg/config"
	"net/http"
)

func StartProcessManage() {
	http.NewServeMux()
	http.HandleFunc(config.CheckSyncStatusAPi, checkStatus)
	http.ListenAndServe(fmt.Sprintf(":%d", config.SyncServerPort), nil)
}
