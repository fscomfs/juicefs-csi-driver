package task

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/juicedata/juicefs-csi-driver/pkg/meta"
	"github.com/redis/go-redis/v9"
	"testing"
	"time"
)

func TestDiscovery_DoSync(t *testing.T) {
	client, _ := meta.NewClient("redis://:jsj9091@@192.168.1.131:6379/6")
	res, err := client.HGet(context.Background(), "SYNC_TASK_test-pv_SYNCING", "data-set-1/v2/000/3").Bytes()

	if err != nil && err == redis.Nil {
		fmt.Printf("err:%v", err)
	}
	var status SyncProcessStatus
	err = json.Unmarshal(res, &status)
	fmt.Sprintf("val:%s", res)
}

func TestDiscovery_Done(t *testing.T) {
	go func() {
		defer func() {
			fmt.Printf("222222222\n")
		}()
		fmt.Printf("22222--------------\n")
		time.Sleep(time.Second)
	}()
	go func() {
		defer func() {
			fmt.Printf("111111111\n")
		}()
		fmt.Printf("111111----\n")
		time.Sleep(time.Second)
	}()
	fmt.Printf("----+++================------------------\n")
	time.Sleep(10 * time.Second)
	if true {
		return
	}
	fmt.Printf("33333333333333333\n")

	defer func() {
		fmt.Printf("55555555555555555\n")
	}()

}
