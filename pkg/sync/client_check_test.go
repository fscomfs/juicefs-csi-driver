package sync

import (
	"fmt"
	"github.com/juicedata/juicefs-csi-driver/pkg/sync/task"
	"github.com/vbauerster/mpb/v7"
	"github.com/vbauerster/mpb/v7/decor"
	"os"
	"path"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestBar(t *testing.T) {
	var wg sync.WaitGroup
	// passed wg will be accounted at p.Wait() call
	p := mpb.New(mpb.WithWaitGroup(&wg), mpb.WithOutput(os.Stdout))
	total, numBars := 100, 3
	wg.Add(numBars)
	status := make(map[int]chan task.ProcessStat)
	for i := 0; i < numBars; i++ {
		name := fmt.Sprintf("Bar#%d:", i)
		s := make(chan task.ProcessStat, 1)
		status[i] = s
		decors := []decor.Decorator{
			newTitleDecorator(name, s),
			decor.Merge(decor.CurrentNoUnit("%d", decor.WCSyncSpaceR), decor.WCSyncSpaceR),
		}
		bar := p.Add(0, newSpinner(),
			mpb.BarFillerClearOnComplete(),
			mpb.PrependDecorators(decors...),
		)
		// simulating some work
		go func() {
			defer wg.Done()
			for i := 0; i < total; i++ {
				bar.SetCurrent(int64(total))
				bar.Increment()
				time.Sleep(500 * time.Millisecond)
			}
		}()
	}

	wg.Wait()

}

func Test_path(t *testing.T) {
	t.Logf(path.Dir("1/2/3"))
	t.Logf("%v", strings.HasSuffix("1/2/3/", "/"))
	t.Log(path.Join("a", "b"))

	os.MkdirAll("/a/b/c", 0755)

	os.RemoveAll("/a/b/c")

}
