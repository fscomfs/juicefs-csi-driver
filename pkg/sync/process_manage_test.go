package sync

import (
	"github.com/juicedata/juicefs-csi-driver/pkg/config"
	"testing"
)

func TestStartProcessManage(t *testing.T) {
	config.SyncServerPort = 8080
	startProcessManage()
}
