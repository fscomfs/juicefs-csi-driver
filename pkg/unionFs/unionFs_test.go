package unionFs

import (
	"fmt"
	"golang.org/x/sys/unix"
	k8sMount "k8s.io/utils/mount"
	"os"
	"strings"
	"testing"
)

func Test_overlay(t *testing.T) {
	target := "/data/target"
	source := "overlay"
	fstype := "overlay"
	flags := 0

	// 挂载选项，例如 lowerdir、upperdir 和 workdir
	options := "lowerdir=/data/lower:/data/lower2,upperdir=/data/diff1,workdir=/data/work,lns=UTF-8"

	if err := unix.Mount(source, target, fstype, uintptr(flags), options); err != nil {
		fmt.Printf("挂载Overlay文件系统失败：%v\n", err)
		return
	}

	fmt.Println("Overlay文件系统成功挂载到", target)

	// 在完成后，确保卸载文件系统
	defer func() {
		if err := unix.Unmount(target, 0); err != nil {
			fmt.Printf("卸载Overlay文件系统失败：%v\n", err)
		} else {
			fmt.Println("Overlay文件系统已卸载")
		}
	}()
}

func Test_status(t *testing.T) {
	fileInfo, err := os.Open("/var/lib/kubelet/pods/08ce9b0b-358c-4f10-881b-98aac7da3654/volumes/kubernetes.io~csi/test-pv/mount")
	defer fileInfo.Close()
	if err != nil {
		fmt.Printf("%+v\n", err)
	}
	fmt.Printf("%+v\n", fileInfo)

	_, err = fileInfo.ReadDir(0)
	if err != nil {
		fmt.Println("无法读取目录内容:", err)
		return
	}
}

func Test_mountInfo(t *testing.T) {
	mis, err := k8sMount.ParseMountInfo("/proc/self/mountinfo")
	if err != nil {

	}
	for i := range mis {
		if strings.HasSuffix(mis[i].MountPoint, "/mount") {
			fmt.Println(mis[i])
		}
	}
}
