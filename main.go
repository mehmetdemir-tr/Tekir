package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
)

func main() {
	if len(os.Args) < 2 {
		usage()
	}
	switch os.Args[1] {
	case "run":
		parent()
	case "child":
		child()
	default:
		panic("Miyaaauvvv, yardıım!")
	}
}



func usage() {
	fmt.Fprintf(
		os.Stderr,
		`Tekir - Lokal konteyner servisiniz :)

Kullanım:
  %s run <initramfs veya .iso> <komut> [argümanlar...]

Örnek:
  sudo %s run debian.iso /bin/sh

`,
		os.Args[0],
		os.Args[0],
	)

	os.Exit(1)
}

func parent() {
	initramfs, err := filepath.Abs(os.Args[2])
	if err != nil {
		panic(err)
	}
	if _, err := os.Stat(initramfs); err != nil {
		panic(fmt.Errorf("initramfs bulunamadı: %w", err))
	}
	args := append(
		[]string{"child", initramfs},
		os.Args[3:]...,
	)
	cmd := exec.Command("/proc/self/exe", args...)
	cmd.SysProcAttr = &syscall.SysProcAttr{
		Cloneflags: syscall.CLONE_NEWUTS | syscall.CLONE_NEWPID | syscall.CLONE_NEWNS,
	}
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	if err := cmd.Run(); err != nil {
		fmt.Println("Hata:", err)
		os.Exit(1)
	}
}
func child() {
	imagePath := os.Args[2]
	command := os.Args[3]
	commandArgs := os.Args[4:]

	rootfs, err := os.MkdirTemp(".", "rootfs-")
	if err != nil {
		panic(fmt.Errorf("rootfs oluşturulamadı: %w", err))
	}

	defer func() {
		syscall.Unmount(filepath.Join(rootfs, "proc"), syscall.MNT_DETACH)
		syscall.Unmount(rootfs, syscall.MNT_DETACH)
		os.RemoveAll(rootfs)
	}()

	if filepath.Ext(imagePath) == ".iso" {
		isoMountPoint, err := os.MkdirTemp(".", "iso-mount-")
		if err != nil {
			panic(fmt.Errorf("iso mount dizini oluşturulamadı: %w", err))
		}
		
		defer func() {
			exec.Command("umount", "-f", isoMountPoint).Run()
			os.RemoveAll(isoMountPoint)
		}()

		cmdMount := exec.Command("mount", "-o", "loop,ro", imagePath, isoMountPoint)
		if err := cmdMount.Run(); err != nil {
			panic(fmt.Errorf("ISO mount edilemedi: %w", err))
		}

		targetInitramfs := filepath.Join(isoMountPoint, "boot", "initramfs.cpio.gz")
		if _, err := os.Stat(targetInitramfs); err != nil {
			panic(fmt.Errorf("ISO açıldı ancak içinde 'boot/initramfs.cpio.gz' bulunamadı: %w", err))
		}

		if err := extractInitramfs(targetInitramfs, rootfs); err != nil {
			panic(fmt.Errorf("ISO içindeki initramfs ayıklanamadı: %w", err))
		}

	} else {
		if err := extractInitramfs(imagePath, rootfs); err != nil {
			panic(err)
		}
	}

	if err := syscall.Mount("", "/", "", syscall.MS_PRIVATE|syscall.MS_REC, ""); err != nil {
		panic(fmt.Errorf("root mount private yapılamadı: %w", err))
	}

	if err := os.Chdir(rootfs); err != nil {
		panic(fmt.Errorf("rootfs dizinine girilemedi: %w", err))
	}

	if err := syscall.Chroot("."); err != nil {
		panic(fmt.Errorf("chroot başarısız: %w", err))
	}

	if err := os.Chdir("/"); err != nil {
		panic(fmt.Errorf("yeni root'a geçilemedi: %w", err))
	}

	os.Mkdir("/proc", 0555)
	syscall.Mount("proc", "/proc", "proc", 0, "")

	/*argv := append([]string{command}, commandArgs...)
	if err := syscall.Exec(command, argv, os.Environ()); err != nil {
		fmt.Println("Hata:", err)
		os.Exit(1)
	}*/

	os.Mkdir("/tmp", 0777)
	syscall.Mount("tmpfs", "/tmp", "tmpfs", 0, "")
	os.Mkdir("/run", 0777)
	syscall.Mount("tmpfs", "/run", "tmpfs", 0, "")

	containerEnv := []string{
		"PATH=/bin:/sbin:/usr/bin:/usr/sbin",
		"TERM=linux",
		"HOME=/root",
	}

	argv := append([]string{command}, commandArgs...)
	if err := syscall.Exec(command, argv, containerEnv); err != nil {
		fmt.Println("Hata:", err)
		os.Exit(1)
	}
}



func extractInitramfs(archivePath, destination string) error {
	input, err := os.Open(archivePath)
	if err != nil {
		return fmt.Errorf("initramfs açılamadı: %w", err)
	}
	defer input.Close()

	gzipCmd := exec.Command("gzip", "-dc")
	gzipCmd.Stdin = input

	cpioCmd := exec.Command(
		"cpio",
		"--extract",
		"--make-directories",
		"--preserve-modification-time",
		"--no-absolute-filenames",
	)
	cpioCmd.Dir = destination
	cpioCmd.Stdin, err = gzipCmd.StdoutPipe()
	if err != nil {
		return fmt.Errorf("pipe oluşturulamadı: %w", err)
	}

	gzipCmd.Stderr = os.Stderr
	cpioCmd.Stderr = os.Stderr

	if err := cpioCmd.Start(); err != nil {
		return fmt.Errorf("cpio başlatılamadı: %w", err)
	}

	if err := gzipCmd.Start(); err != nil {
		return fmt.Errorf("gzip başlatılamadı: %w", err)
	}

	if err := gzipCmd.Wait(); err != nil {
		return fmt.Errorf("gzip başarısız: %w", err)
	}

	if err := cpioCmd.Wait(); err != nil {
		return fmt.Errorf("cpio başarısız: %w", err)
	}

	return nil
}

func must(err error) {
	if err != nil {
		panic(err)
	}
}
