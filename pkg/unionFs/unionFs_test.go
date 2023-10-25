package unionFs

import (
	"fmt"
	"golang.org/x/sys/unix"
	"testing"
)

func Test_overlay(t *testing.T) {
	target := "/data/target"
	source := "overlay"
	fstype := "overlay"
	flags := 0

	// 挂载选项，例如 lowerdir、upperdir 和 workdir
	options := "lowerdir=/data/lower:/data/lower2,upperdir=/data/diff1,workdir=/data/work"

	if err := unix.Mount(source, target, fstype, uintptr(flags), options); err != nil {
		fmt.Printf("挂载Overlay文件系统失败：%v\n", err)
		return
	}

	fmt.Println("Overlay文件系统成功挂载到", target)

	//// 在完成后，确保卸载文件系统
	//defer func() {
	//	if err := unix.Unmount(target, 0); err != nil {
	//		fmt.Printf("卸载Overlay文件系统失败：%v\n", err)
	//	} else {
	//		fmt.Println("Overlay文件系统已卸载")
	//	}
	//}()
}
