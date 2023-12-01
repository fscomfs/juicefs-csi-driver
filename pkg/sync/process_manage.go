package sync

import (
	"fmt"
	"github.com/juicedata/juicefs-csi-driver/pkg/config"
	"net/http"
)

func startProcessManage() {
	http.NewServeMux()
	http.HandleFunc(config.CheckSyncStatusApi, checkStatus)
	http.HandleFunc(config.SyncProcessStateApi, processState)
	http.ListenAndServe(fmt.Sprintf(":%d", config.SyncServerPort), nil)
}
