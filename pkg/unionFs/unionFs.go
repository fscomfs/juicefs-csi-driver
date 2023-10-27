/*
Copyright 2021 Juicedata Inc

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package unionFs

import (
	"bufio"
	"context"
	"fmt"
	"github.com/juicedata/juicefs-csi-driver/pkg/util"
	"github.com/opencontainers/selinux/go-selinux/label"
	"github.com/pkg/errors"
	"golang.org/x/sys/unix"
	"io/ioutil"
	"k8s.io/klog"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"strings"
	"time"

	"k8s.io/utils/mount"
)

const (
	defaultCheckTimeout = 2 * time.Second
	fsTypeNone          = "none"
	procMountInfoPath   = "/proc/self/mountinfo"
	unionBashPath       = "/var/lib/kubelet/union"
)

// Interface of juicefs provider
type Interface interface {
	mount.Interface
	UnionMount(ctx context.Context, target string) error
	UnionUnmount(ctx context.Context, mountPath string) error
	CreateUnionLayers(ctx context.Context) error
}

type unionFs struct {
	mount.SafeFormatAndMount
	podId           string
	uniqueId        string
	lowerPath       []string
	lowerLayers     []string
	rwLayer         string
	workLayer       string
	supportsOverlay bool
	supportsAufs    bool
}

var (
	ErrAufsNotSupported = fmt.Errorf("AUFS was not found in /proc/filesystems")
)

func CreateUnionFs(lowerPath []string, podId, uniqueId string) *unionFs {
	return &unionFs{
		lowerPath: lowerPath,
		podId:     podId,
		uniqueId:  uniqueId,
	}
}

func (u *unionFs) CreateUnionLayers(ctx context.Context) (err error) {
	base := path.Join(unionBashPath, u.podId, u.uniqueId)
	defer func() {
		if err != nil {
			os.RemoveAll(base)
		}
	}()

	s := removeSubPaths(u.lowerPath)
	for _, val := range s {
		u.lowerLayers = append(u.lowerLayers, val)
	}
	//rwlay
	u.rwLayer = path.Join(base, "rw")
	u.workLayer = path.Join(base, "work")
	exists := false
	if err := util.DoWithTimeout(ctx, defaultCheckTimeout, func() (err error) {
		exists, err = mount.PathExists(u.rwLayer)
		return
	}); err != nil {
		return fmt.Errorf("could not check rwLayer %q exists: %v", u.rwLayer, err)
	}
	if !exists {
		err = os.MkdirAll(u.rwLayer, os.FileMode(0777))
		if err != nil {
			return err
		}
	}
	if err := util.DoWithTimeout(ctx, defaultCheckTimeout, func() (err error) {
		exists, err = mount.PathExists(u.workLayer)
		return
	}); err != nil {
		return fmt.Errorf("could not check workLayer %q exists: %v", u.workLayer, err)
	}
	if !exists {
		err = os.MkdirAll(u.workLayer, os.FileMode(0777))
		if err != nil {
			return err
		}
	}
	return nil
}

func (u *unionFs) aufsMount(target string) (err error) {
	defer func() {
		if err != nil {

		}
	}()
	offset := 54
	b := make([]byte, unix.Getpagesize()-offset) // room for xino & mountLabel
	bp := copy(b, fmt.Sprintf("br:%s=rw", u.rwLayer))
	for index := 0; index < len(u.lowerLayers); index++ {
		layer := fmt.Sprintf(":%s=ro+wh", u.lowerLayers[index])
		if bp+len(layer) > len(b) {
			break
		}
		bp += copy(b[bp:], layer)
	}
	opts := "dio,xino=/dev/shm/aufs.xino"
	data := label.FormatMountLabel(fmt.Sprintf("%s,%s", string(b[:bp]), opts), "")
	err = unix.Mount("none", target, "aufs", 0, data)
	if err != nil {
		err = errors.Wrap(err, "mount target="+target+" data="+data)
		return
	} else {
		klog.V(5).Infof("success mount target=%s data=%v", target, data)
	}
	return
}

func (u *unionFs) overlayMount(target string) (err error) {
	opts := "lowerdir=" + strings.Join(u.lowerLayers, ":") + ",upperdir=" + u.rwLayer + ",workdir=" + u.workLayer
	klog.V(5).Infof("Overlay mount target:%v,opts:%v", target, opts)
	if err := unix.Mount("overlay", target, "overlay", uintptr(0), opts); err != nil {
		return err
	}
	return
}

func supportsAufs() error {
	// We can try to modprobe aufs first before looking at
	// proc/filesystems for when aufs is supported
	exec.Command("modprobe", "aufs").Run()
	f, err := os.Open("/proc/filesystems")
	if err != nil {
		return err
	}
	defer f.Close()

	s := bufio.NewScanner(f)
	for s.Scan() {
		if strings.Contains(s.Text(), "aufs") {
			return nil
		}
	}
	return ErrAufsNotSupported
}

func supportsOverlay(d string) error {
	td, err := ioutil.TempDir(d, "check-overlayfs-support")
	if err != nil {
		return err
	}
	defer func() {
		if err := os.RemoveAll(td); err != nil {
			klog.V(5).Infof("Failed to remove check directory %v: %v", td, err)
		}
	}()

	for _, dir := range []string{"lower1", "lower2", "upper", "work", "merged"} {
		if err := os.Mkdir(filepath.Join(td, dir), 0755); err != nil {
			return err
		}
	}

	mnt := filepath.Join(td, "merged")
	lowerDir := path.Join(td, "lower2")
	opts := fmt.Sprintf("lowerdir=%s,upperdir=%s,workdir=%s", lowerDir, path.Join(td, "upper"), path.Join(td, "work"))
	if err := unix.Mount("overlay", mnt, "overlay", 0, opts); err != nil {
		return errors.Wrap(err, "failed to mount overlay")
	}
	if err := unix.Unmount(mnt, 0); err != nil {
		klog.V(5).Infof("Failed to unmount check directory %v: %v", mnt, err)
	}
	return nil
}
func (u *unionFs) UnionMount(ctx context.Context, target string) error {
	if err := supportsOverlay(target); err == nil {
		klog.V(5).Infof("target  supper overlay do overlay mount")
		u.supportsOverlay = true
		return u.overlayMount(target)
	} else {
		klog.V(5).Infof("target not supper overlay %v", err)
	}
	if err := supportsAufs(); err == nil {
		klog.V(5).Infof("target  supper aufs do aufs mount %v", err)
		u.supportsAufs = true
		return u.aufsMount(target)
	} else {
		klog.V(5).Infof("target not supper aufs %v", err)
	}
	return fmt.Errorf("Union mount fail")
}

func (u *unionFs) UnionUnmount(ctx context.Context, mountPath string) error {
	if err := unix.Unmount(mountPath, 0); err != nil {
		return err
	}
	base := path.Join(unionBashPath, u.podId, u.uniqueId)
	os.RemoveAll(base)
	return nil
}

var _ Interface = &unionFs{}

func removeSubPaths(paths []string) []string {
	shortestPaths := make(map[string]bool)
	for _, path := range paths {
		isSubPath := false
		for existingPath := range shortestPaths {
			if isSubpath(existingPath, path) {
				isSubPath = true
				break
			}
		}
		if !isSubPath {
			shortestPaths[path] = true
		}
	}
	result := make([]string, 0, len(shortestPaths))
	for shortestPath := range shortestPaths {
		result = append(result, shortestPath)
	}
	return result
}

func isSubpath(path1, path2 string) bool {
	cleanPath1 := filepath.Clean(path1)
	cleanPath2 := filepath.Clean(path2)
	return cleanPath1 == cleanPath2 || filepath.HasPrefix(cleanPath1, cleanPath2+string(filepath.Separator))
}
