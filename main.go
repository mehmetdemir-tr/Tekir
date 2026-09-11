package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"unsafe"
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
  %s run <initramfs.cpio.gz> <komut> [argümanlar...]

Örnek:
  sudo %s run initramfs.cpio.gz /bin/sh

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
	rootfs, err := os.MkdirTemp("/tmp/", "rootfs-")
	if err != nil {
		panic(fmt.Errorf("rootfs oluşturulamadı: %w", err))
	}
	defer os.RemoveAll(rootfs)
	args := append(
		[]string{"child", initramfs, rootfs},
		os.Args[3:]...,
	)
	cmd := exec.Command("/proc/self/exe", args...)
	cmd.SysProcAttr = &syscall.SysProcAttr{
		Cloneflags: syscall.CLONE_NEWUTS | syscall.CLONE_NEWPID | syscall.CLONE_NEWNS | syscall.CLONE_NEWIPC | syscall.CLONE_NEWNET,
	}
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	if err := cmd.Start(); err != nil {
		fmt.Println("Hata:", err)
		os.Exit(1)
	}
	pid := cmd.Process.Pid

	if err := setupNetwork(pid); err != nil {
		fmt.Fprintf(os.Stderr, "Ağ bağlantısı kurulamadı: %v\n", err)
	} else {
		defer cleanupNetwork()
	}

	if err := cmd.Wait(); err != nil {
		fmt.Println("Hata:", err)
		os.Exit(1)
	}
}
func run(name string, args ...string) error {
	c := exec.Command(name, args...)
	c.Stdout = os.Stdout
	c.Stderr = os.Stderr
	return c.Run()
}
func setupNetwork(pid int) error {
	pidStr := fmt.Sprintf("%d", pid)
	hostIf := "tekir-h"
	contIf := "tekir-c"
	hostIP := "10.88.0.1/24"
	contIP := "10.88.0.2/24"
	gw := "10.88.0.1"

	exec.Command("ip", "link", "del", hostIf).Run()

	if err := run("ip", "link", "add", hostIf, "type", "veth", "peer", "name", contIf); err != nil {
		return fmt.Errorf("veth yaratılamadı: %w", err)
	}
	if err := run("ip", "link", "set", contIf, "netns", pidStr); err != nil {
		return fmt.Errorf("veth netns'e taşınamadı: %w", err)
	}
	if err := run("ip", "addr", "add", hostIP, "dev", hostIf); err != nil {
		return err
	}
	if err := run("ip", "link", "set", hostIf, "up"); err != nil {
		return err
	}

	os.WriteFile("/proc/sys/net/ipv4/ip_forward", []byte("1"), 0644)

	c := exec.Command("iptables", "-t", "nat", "-C", "POSTROUTING", "-s", "10.88.0.0/24", "-j", "MASQUERADE")
	if c.Run() != nil {
		if err := run("iptables", "-t", "nat", "-A", "POSTROUTING", "-s", "10.88.0.0/24", "-j", "MASQUERADE"); err != nil {
			return fmt.Errorf("iptables NAT başarısız: %w", err)
		}
	}

	run("iptables", "-I", "FORWARD", "1", "-i", hostIf, "-j", "ACCEPT")
	run("iptables", "-I", "FORWARD", "1", "-o", hostIf, "-j", "ACCEPT")

	ns := func(ipArgs ...string) error {
		full := append([]string{"-t", pidStr, "-n", "ip"}, ipArgs...)
		return run("nsenter", full...)
	}

	if err := ns("link", "set", contIf, "name", "eth0"); err != nil {
		return fmt.Errorf("rename eth0 başarısız: %w", err)
	}
	if err := ns("addr", "add", contIP, "dev", "eth0"); err != nil {
		return err
	}
	if err := ns("link", "set", "eth0", "up"); err != nil {
		return err
	}
	if err := ns("route", "add", "default", "via", gw); err != nil {
		return err
	}

	return nil
}

func cleanupNetwork() {
	exec.Command("ip", "link", "del", "tekir-h").Run()
}

func child() {
	imagePath := os.Args[2]
	rootfs := os.Args[3]
	command := os.Args[4]
	commandArgs := os.Args[5:]

	defer func() {
		syscall.Unmount(filepath.Join(rootfs, "proc"), syscall.MNT_DETACH)
		syscall.Unmount(rootfs, syscall.MNT_DETACH)
	}()

	if filepath.Ext(imagePath) == ".iso" {
		isoMountPoint, err := os.MkdirTemp("/tmp", "iso-mount-")
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
		fmt.Fprintf(os.Stderr,"uyarı:root mount private yapılamadı (o zalım olası nested olabilir): %v\n", err)	
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

	syscall.Sethostname([]byte("tekir"))
	mustMount := func(src, target, fstype string, flags uintptr) {
		os.MkdirAll(target, 0755)
		if err := syscall.Mount(src, target, fstype, flags, ""); err != nil {
			fmt.Fprintf(os.Stderr, "mount %s -> %s başarısız: %v\n", src, target, err)
		}
	}

	mustMount("proc", "/proc", "proc", syscall.MS_NOSUID|syscall.MS_NOEXEC|syscall.MS_NODEV)
	mustMount("sysfs", "/sys", "sysfs", syscall.MS_NOSUID|syscall.MS_NOEXEC|syscall.MS_NODEV|syscall.MS_RDONLY)
	mustMount("tmpfs", "/dev", "tmpfs", syscall.MS_NOSUID|syscall.MS_NOEXEC)
	os.MkdirAll("/dev/pts", 0755)
	os.MkdirAll("/dev/shm", 0755)
	syscall.Mount("devpts", "/dev/pts", "devpts", syscall.MS_NOSUID|syscall.MS_NOEXEC, "newinstance,ptmxmode=666,mode=620")
	syscall.Mount("tmpfs", "/dev/shm", "tmpfs", syscall.MS_NOSUID|syscall.MS_NODEV, "")

	devs := []struct {
		path string
		mode uint32
		dev int
	} {
		{"/dev/null", 0666, 0x103},    // 1,3
		{"/dev/zero", 0666, 0x105},    // 1,5
		{"/dev/full", 0666, 0x107},    // 1,7
		{"/dev/random", 0666, 0x108},  // 1,8
		{"/dev/urandom", 0666, 0x109}, // 1,9
		{"/dev/tty", 0666, 0x500},     // 5,0

	}
	for _, d := range devs {
		os.Remove(d.path)
		syscall.Mknod(d.path, d.mode|syscall.S_IFCHR, d.dev)
	}
	os.Symlink("/proc/self/fd", "/dev/fd")
	os.Symlink("/proc/self/fd/0", "/dev/stdin")
	os.Symlink("/proc/self/fd/1", "/dev/stdout")
	os.Symlink("/proc/self/fd/2", "/dev/stderr")

	mustMount("tmpfs", "/tmp", "tmpfs", syscall.MS_NOSUID|syscall.MS_NODEV)
	os.Chmod("/tmp", 0777)
	mustMount("tmpfs", "/run", "tmpfs", syscall.MS_NOSUID|syscall.MS_NODEV)

	os.MkdirAll("/etc", 0755)
	if data, err := os.ReadFile("/etc/resolv.conf"); err == nil {
		os.WriteFile("/etc/resolv.conf", data, 0644)
	} else {
		os.WriteFile("/etc/resolv.conf", []byte("nameserver 8.8.8.8\n"), 0644)
	}

	if err := bringLoopbackUp(); err != nil {
		fmt.Fprintf(os.Stderr, "lo açılamadı: %v\n", err)
	}

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



func bringLoopbackUp() error {
	fd, err := syscall.Socket(syscall.AF_INET, syscall.SOCK_DGRAM, 0)
	if err != nil {
		return err
	}
	defer syscall.Close(fd)

	const (
		SIOCGIFFLAGS = 0x8913
		SIOCSIFFLAGS = 0x8914
		IFF_UP       = 0x1
		IFF_RUNNING  = 0x40
	)

	var ifr [40]byte
	copy(ifr[:], "lo")

	_, _, errno := syscall.Syscall(
		syscall.SYS_IOCTL,
		uintptr(fd),
		uintptr(SIOCGIFFLAGS),
		uintptr(unsafe.Pointer(&ifr[0])),
	)
	if errno != 0 {
		return errno
	}

	flags := *(*uint16)(unsafe.Pointer(&ifr[16]))
	flags |= IFF_UP | IFF_RUNNING
	*(*uint16)(unsafe.Pointer(&ifr[16])) = flags

	_, _, errno = syscall.Syscall(
		syscall.SYS_IOCTL,
		uintptr(fd),
		uintptr(SIOCSIFFLAGS),
		uintptr(unsafe.Pointer(&ifr[0])),
	)
	if errno != 0 {
		return errno
	}
	return nil
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
		"-i",
		"-d",
		"-m",
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
