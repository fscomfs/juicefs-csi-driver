package task

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"github.com/juicedata/juicefs-csi-driver/pkg/config"
	"github.com/redis/go-redis/v9"
	"k8s.io/klog"
	"net/http"
	"time"
)

const API = "/discovery"

type Discovery struct {
	URL                string
	mixture            string
	metaUrl            string
	metaAccessKey      string
	metaAccessPassword string
	client             *http.Client
	metaClient         *redis.Client
}

type SyncProcessStatus struct {
	DstPath    string `json:"dst_path"`
	SourcePath string `json:"source_path"`
	SyncStatus int    `json:"sync_status"` //1 sync ing //2 sync fail //3 sync completed //4 sync no need
	Total      int64  `json:"total"`       //sync process 0.1
	Current    int64  `json:"current"`
}

type DataItem struct {
	SourcePath string `json:"source_path"`
	TargetPV   string `json:"target_pv"`
}
type PlatformResponse struct {
	Code    string     `json:"code"`
	Data    []DataItem `json:"data"`
	Message string     `json:"message"`
}

type DiscoveryParam struct {
	Mixture string `json:"mixture"`
}

func (d *Discovery) PollAndStore() {
	ticker := time.NewTimer(5 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:

		}
	}
}

func (d *Discovery) DiscoverFromPlatform(ctx context.Context) {
	param := DiscoveryParam{
		Mixture: d.mixture,
	}
	p, _ := json.Marshal(param)
	req, err := http.NewRequest(http.MethodPost, d.URL+API, bytes.NewBuffer(p))
	if err != nil {
	}
	var resData PlatformResponse
	pv := config.SyncController.PV
	metaKey := fmt.Sprintf("%s_data_set", pv)
	if res, err := d.client.Do(req); err == nil {
		if res.StatusCode == http.StatusOK {
			if err := json.NewDecoder(res.Body).Decode(&resData); err != nil {
				klog.V(5).Infof("[DiscoverFromPlatform] request fail %v", err)
				return
			}
		} else {
			return
		}
	} else {
		return
	}
	storedDataSet, err := d.metaClient.HGetAll(ctx, metaKey).Result()
	if err != nil {
		return
	}
	for _, v := range resData.Data {
		if _, ok := storedDataSet[v.SourcePath]; !ok {
			err := d.metaClient.HSet(ctx, metaKey, v.SourcePath, "").Err()
			if err != nil {
				continue
			}
		}
	}

}
