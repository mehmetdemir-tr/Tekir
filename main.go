// Copyright (C) 2026 mehmetdemir-tr
// 
// This program is free software: you can redistribute it and/or modify
// it under the terms of the GNU General Public License as published by
// the Free Software Foundation, either version 3 of the License.


package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
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
	case "ps":
		psCmd()
	case "stop":
		stopCmd()
	default:
		panic("Miyaaauvvv, yardıım!")
	}
}

func usage() {
	fmt.Fprintf(
		os.Stderr,
		`Tekir - Lokal konteyner servisiniz :)

Kullanım:
  %s run [--name AD] [-e KEY=VAL]... [--memory 512M] [--cpus 1.5] [--nonet] <initramfs.cpio.gz|image.iso> <komut> [argümanlar...]
  %s ps
  %s stop <ad>

Örnek:
  sudo %s run initramfs.cpio.gz /bin/sh
  sudo %s run --name web -e PORT=80 --memory 512M initramfs.cpio.gz /bin/sh

`,
		os.Args[0],
		os.Args[0],
		os.Args[0],
		os.Args[0],
		os.Args[0],
	)

	os.Exit(1)
}

func parent() {
	if !hasCaps() {
		fmt.Fprintln(os.Stderr, "tekir nested/capless ortamda calismaz: mount ve network icin yetki gerekli")
		os.Exit(1)
	}
	name, extraEnv, memBytes, cpuMax, noNet, rest := parseRunFlags(os.Args[2:])
	if len(rest) < 2 {
		usage()
	}
	initramfs, err := filepath.Abs(rest[0])
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
	if len(extraEnv) > 0 {
		os.WriteFile(filepath.Join(rootfs, "env"), []byte(joinLines(extraEnv)), 0644)
	}
	lower, err := ensureLower(initramfs)
	if err != nil {
		panic(err)
	}
	upper := filepath.Join(rootfs, "upper")
	work := filepath.Join(rootfs, "work")
	merged := filepath.Join(rootfs, "merged")
	for _, d := range []string{upper, work, merged} {
		if err := os.MkdirAll(d, 0755); err != nil {
			panic(fmt.Errorf("overlay dizini açılamadı: %w", err))
		}
	}
	uid, _ := strconv.Atoi(numericOr(os.Getenv("SUDO_UID"), "65534"))
	gid, _ := strconv.Atoi(numericOr(os.Getenv("SUDO_GID"), "65534"))
	filepath.Walk(rootfs, func(p string, info os.FileInfo, err error) error {
		if err == nil {
			os.Lchown(p, uid, gid)
		}
		return nil
	})
	os.Chmod(rootfs, 0777)
	overlayOpts := fmt.Sprintf("lowerdir=%s,upperdir=%s,workdir=%s,userxattr", lower, upper, work)
	if err := syscall.Mount("overlay", merged, "overlay", 0, overlayOpts); err != nil {
		fmt.Fprintf(os.Stderr, "uyarı: parent overlay kuramadı, child deneyecek: %v\n", err)
	} else {
		defer syscall.Unmount(merged, syscall.MNT_DETACH)
	}
	args := append(
		[]string{"child", initramfs, lower, upper, work, merged},
		rest[1:]...,
	)
	cmd := exec.Command("/proc/self/exe", args...)
	cmd.SysProcAttr = &syscall.SysProcAttr{
		Cloneflags: syscall.CLONE_NEWUTS | syscall.CLONE_NEWPID | syscall.CLONE_NEWNS | syscall.CLONE_NEWIPC | syscall.CLONE_NEWNET,
	}
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		fmt.Fprintln(os.Stderr, "tekir baslatilamadi (capless ortamda nested calismaz):", err)
		os.Exit(1)
	}
	pid := cmd.Process.Pid
	if name == "" {
		name = fmt.Sprintf("tekir-%d", pid)
	} else {
		name = sanitizeName(name)
	}
	if err := stateWrite(name, pid); err != nil {
		fmt.Fprintf(os.Stderr, "uyarı: state yazılamadı: %v\n", err)
	} else {
		defer stateRemove(name)
	}
	cgdir := ""
	if memBytes > 0 || cpuMax != "" {
		if dir, err := setupCgroup(name, pid, memBytes, cpuMax); err != nil {
			fmt.Fprintf(os.Stderr, "uyarı: cgroup kurulamadı: %v\n", err)
		} else {
			cgdir = dir
			defer os.Remove(cgdir)
		}
	}
	hostIf := ""
	if !noNet {
		ready := filepath.Join(rootfs, "ready")
		gofile := filepath.Join(rootfs, "go")
		os.Remove(ready)
		os.Remove(gofile)
		waitForFile(ready, 5)
		id := nextSubnetOctet()
		var err error
		hostIf, err = setupNetwork(pid, id)
		os.WriteFile(gofile, []byte("go"), 0644)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Ağ bağlantısı kurulamadı: %v\n", err)
		} else {
			fmt.Fprintf(os.Stderr, "tekir: %s 10.88.%d.2 %s\n", name, id, hostIf)
		}
	} else {
		os.WriteFile(filepath.Join(rootfs, "go"), []byte("go"), 0644)
	}
	if err := cmd.Wait(); err != nil {
		fmt.Println("Hata:", err)
		os.Exit(1)
	}
	if hostIf != "" {
		cleanupNetwork(hostIf)
	}
}
func run(name string, args ...string) error {
	c := exec.Command(name, args...)
	c.Stdout = os.Stdout
	c.Stderr = os.Stderr
	return c.Run()
}

func numericOr(s, def string) string {
	if s == "" {
		return def
	}
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return def
		}
	}
	return s
}

func waitForFile(path string, timeoutSec int) bool {
	for i := 0; i < timeoutSec*100; i++ {
		if _, err := os.Stat(path); err == nil {
			return true
		}
		syscall.Nanosleep(&syscall.Timespec{Nsec: 10 * 1000 * 1000}, nil)
	}
	return false
}

func sanitizeName(s string) string {
	out := make([]byte, 0, len(s))
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '-' || c == '_' || c == '.' {
			out = append(out, c)
		} else {
			out = append(out, '_')
		}
	}
	if len(out) == 0 {
		return "image"
	}
	return string(out)
}

func ensureLower(imagePath string) (string, error) {
	st, err := os.Stat(imagePath)
	if err != nil {
		return "", fmt.Errorf("image bulunamadı: %w", err)
	}
	key := fmt.Sprintf("%s-%d-%d", sanitizeName(filepath.Base(imagePath)), st.Size(), st.ModTime().Unix())
	lower := filepath.Join("/var/lib/tekir/lower", key)
	done := filepath.Join(lower, ".tekir-done")
	if _, err := os.Stat(done); err == nil {
		fmt.Fprintf(os.Stderr, "tekir: cache kullanılıyor: %s\n", lower)
		return lower, nil
	}

	archive := imagePath
	var isoCleanup func()
	if filepath.Ext(imagePath) == ".iso" {
		mp, err := os.MkdirTemp("/tmp", "iso-mount-")
		if err != nil {
			return "", err
		}
		isoCleanup = func() {
			exec.Command("umount", "-f", mp).Run()
			os.RemoveAll(mp)
		}
		defer isoCleanup()
		if err := exec.Command("mount", "-o", "loop,ro", imagePath, mp).Run(); err != nil {
			return "", fmt.Errorf("ISO mount edilemedi: %w", err)
		}
		archive = filepath.Join(mp, "boot", "initramfs.cpio.gz")
		if _, err := os.Stat(archive); err != nil {
			return "", fmt.Errorf("ISO içinde boot/initramfs.cpio.gz yok: %w", err)
		}
	}

	tmp := lower + ".tmp"
	os.RemoveAll(tmp)
	if err := os.MkdirAll(tmp, 0755); err != nil {
		return "", err
	}
	ok := false
	defer func() {
		if !ok {
			os.RemoveAll(tmp)
		}
	}()
	fmt.Fprintf(os.Stderr, "%s -> %s\n", archive, tmp)
	if err := extractInitramfs(archive, tmp); err != nil {
		return "", err
	}
	if err := os.WriteFile(filepath.Join(tmp, ".tekir-done"), []byte(key), 0644); err != nil {
		return "", err
	}
	if err := os.MkdirAll("/var/lib/tekir/lower", 0755); err != nil {
		return "", err
	}
	if err := os.Rename(tmp, lower); err != nil {
		if _, err2 := os.Stat(done); err2 == nil {
			ok = true
			return lower, nil
		}
		return "", fmt.Errorf("cache taşınamadı: %w", err)
	}
	ok = true
	return lower, nil
}
func nextSubnetOctet() int {
	os.MkdirAll("/var/lib/tekir", 0755)
	f, err := os.OpenFile("/var/lib/tekir/ipalloc", os.O_RDWR|os.O_CREATE, 0644)
	if err != nil {
		return 2
	}
	defer f.Close()
	syscall.Flock(int(f.Fd()), syscall.LOCK_EX)
	defer syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
	b := make([]byte, 16)
	n, _ := f.Read(b)
	cur := 0
	fmt.Sscanf(string(b[:n]), "%d", &cur)
	if cur < 2 || cur > 254 {
		cur = 2
	}
	nxt := cur + 1
	if nxt > 254 {
		nxt = 2
	}
	f.Truncate(0)
	f.Seek(0, 0)
	fmt.Fprintf(f, "%d", nxt)
	return cur
}
func setupNetwork(pid, id int) (string, error) {
	pidStr := fmt.Sprintf("%d", pid)
	hostIf := fmt.Sprintf("t%dh", pid)
	if len(hostIf) > 15 {
		hostIf = hostIf[:15]
	}
	contIf := fmt.Sprintf("t%dc", pid)
	if len(contIf) > 15 {
		contIf = contIf[:15]
	}
	hostIP := fmt.Sprintf("10.88.%d.1/24", id)
	contIP := fmt.Sprintf("10.88.%d.2/24", id)
	gw := fmt.Sprintf("10.88.%d.1", id)
	if err := run("ip", "link", "add", hostIf, "type", "veth", "peer", "name", contIf); err != nil {
		return "", fmt.Errorf("veth yaratılamadı: %w", err)
	}
	if err := run("ip", "link", "set", contIf, "netns", pidStr); err != nil {
		exec.Command("ip", "link", "del", hostIf).Run()
		return "", fmt.Errorf("veth netns'e taşınamadı: %w", err)
	}
	if err := run("ip", "addr", "add", hostIP, "dev", hostIf); err != nil {
		return hostIf, err
	}
	if err := run("ip", "link", "set", hostIf, "up"); err != nil {
		return hostIf, err
	}
	os.WriteFile("/proc/sys/net/ipv4/ip_forward", []byte("1"), 0644)
	c := exec.Command("iptables", "-t", "nat", "-C", "POSTROUTING", "-s", "10.88.0.0/16", "-j", "MASQUERADE")
	if c.Run() != nil {
		if err := run("iptables", "-t", "nat", "-A", "POSTROUTING", "-s", "10.88.0.0/16", "-j", "MASQUERADE"); err != nil {
			return hostIf, fmt.Errorf("iptables NAT başarısız: %w", err)
		}
	}
	run("iptables", "-I", "FORWARD", "1", "-i", hostIf, "-j", "ACCEPT")
	run("iptables", "-I", "FORWARD", "1", "-o", hostIf, "-j", "ACCEPT")
	ns := func(ipArgs ...string) error {
		full := append([]string{"-t", pidStr, "-n", "ip"}, ipArgs...)
		return run("nsenter", full...)
	}
	if err := ns("link", "set", contIf, "name", "eth0"); err != nil {
		return hostIf, fmt.Errorf("rename eth0 başarısız: %w", err)
	}
	if err := ns("addr", "add", contIP, "dev", "eth0"); err != nil {
		return hostIf, err
	}
	if err := ns("link", "set", "eth0", "up"); err != nil {
		return hostIf, err
	}
	if err := ns("route", "add", "default", "via", gw); err != nil {
		return hostIf, err
	}
	return hostIf, nil
}
func cleanupNetwork(hostIf string) {
	run("iptables", "-D", "FORWARD", "-i", hostIf, "-j", "ACCEPT")
	run("iptables", "-D", "FORWARD", "-o", hostIf, "-j", "ACCEPT")
	exec.Command("ip", "link", "del", hostIf).Run()
}
func hasCaps() bool {
	b, err := os.ReadFile("/proc/self/status")
	if err != nil {
		return true
	}
	for _, l := range splitLinesCR(string(b)) {
		if len(l) > 7 && l[:7] == "CapEff:" {
			for _, c := range l[7:] {
				if c != ' ' && c != '0' && c != '\t' {
					return true
				}
			}
			return false
		}
	}
	return true
}
func splitLinesCR(s string) []string {
	var out []string
	cur := ""
	for i := 0; i < len(s); i++ {
		if s[i] == '\n' {
			out = append(out, cur)
			cur = ""
		} else {
			cur += string(s[i])
		}
	}
	return out
}
func joinLines(a []string) string {
	s := ""
	for _, l := range a {
		s += l + "\n"
	}
	return s
}
func parseRunFlags(a []string) (string, []string, int64, string, bool, []string) {
	name := ""
	var env []string
	var mem int64
	cpu := ""
	noNet := false
	rest := []string{}
	for i := 0; i < len(a); i++ {
		switch a[i] {
		case "--name", "-n":
			i++
			if i < len(a) {
				name = a[i]
			}
		case "-e", "--env":
			i++
			if i < len(a) {
				env = append(env, a[i])
			}
		case "--memory":
			i++
			if i < len(a) {
				mem = parseBytes(a[i])
			}
		case "--cpus":
			i++
			if i < len(a) {
				cpu = parseCpus(a[i])
			}
		case "--nonet":
			noNet = true
		default:
			rest = append(rest, a[i])
		}
	}
	return name, env, mem, cpu, noNet, rest
}
func parseBytes(s string) int64 {
	mult := int64(1)
	num := s
	if len(s) > 1 {
		last := s[len(s)-1]
		if last == 'K' || last == 'k' {
			mult = 1024
			num = s[:len(s)-1]
		} else if last == 'M' || last == 'm' {
			mult = 1024 * 1024
			num = s[:len(s)-1]
		} else if last == 'G' || last == 'g' {
			mult = 1024 * 1024 * 1024
			num = s[:len(s)-1]
		}
	}
	v, _ := strconv.Atoi(num)
	return int64(v) * mult
}
func parseCpus(s string) string {
	f, err := strconv.ParseFloat(s, 64)
	if err != nil || f <= 0 {
		return ""
	}
	return fmt.Sprintf("%d 100000", int(f*100000))
}
func stateWrite(name string, pid int) error {
	if err := os.MkdirAll("/var/run/tekir", 0755); err != nil {
		return err
	}
	p := filepath.Join("/var/run/tekir", name)
	if _, err := os.Stat(p); err == nil {
		return fmt.Errorf("%s zaten var", name)
	}
	return os.WriteFile(p, []byte(fmt.Sprintf("%d", pid)), 0644)
}
func stateRemove(name string) {
	os.Remove(filepath.Join("/var/run/tekir", name))
}
func psCmd() {
	d, err := os.ReadDir("/var/run/tekir")
	if err != nil {
		fmt.Println("çalışan konteyner yok")
		return
	}
	fmt.Printf("%-20s %-8s %s\n", "AD", "PID", "DURUM")
	for _, e := range d {
		b, err := os.ReadFile(filepath.Join("/var/run/tekir", e.Name()))
		if err != nil {
			continue
		}
		pid := 0
		fmt.Sscanf(string(b), "%d", &pid)
		st := "çıkış"
		if _, err := os.Stat(fmt.Sprintf("/proc/%d", pid)); err == nil {
			st = "çalışıyor"
		} else {
			os.Remove(filepath.Join("/var/run/tekir", e.Name()))
		}
		fmt.Printf("%-20s %-8d %s\n", e.Name(), pid, st)
	}
}
func stopCmd() {
	if len(os.Args) < 3 {
		fmt.Fprintln(os.Stderr, "kullanim: tekir stop <ad>")
		os.Exit(1)
	}
	name := sanitizeName(os.Args[2])
	b, err := os.ReadFile(filepath.Join("/var/run/tekir", name))
	if err != nil {
		fmt.Fprintln(os.Stderr, "boyle konteyner yok:", name)
		os.Exit(1)
	}
	pid := 0
	fmt.Sscanf(string(b), "%d", &pid)
	p, err := os.FindProcess(pid)
	if err != nil {
		stateRemove(name)
		return
	}
	p.Signal(syscall.SIGTERM)
	for i := 0; i < 50; i++ {
		if _, err := os.Stat(fmt.Sprintf("/proc/%d", pid)); err != nil {
			break
		}
		syscall.Nanosleep(&syscall.Timespec{Nsec: 100 * 1000 * 1000}, nil)
	}
	if _, err := os.Stat(fmt.Sprintf("/proc/%d", pid)); err != nil {
		stateRemove(name)
	} else {
		p.Kill()
	}
	fmt.Println("durduruldu:", name)
}
func setupCgroup(name string, pid int, memBytes int64, cpuMax string) (string, error) {
	os.MkdirAll("/sys/fs/cgroup/tekir.slice", 0755)
	os.WriteFile("/sys/fs/cgroup/cgroup.subtree_control", []byte("+cpu +memory"), 0644)
	os.WriteFile("/sys/fs/cgroup/tekir.slice/cgroup.subtree_control", []byte("+cpu +memory"), 0644)
	dir := filepath.Join("/sys/fs/cgroup/tekir.slice", name)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return "", err
	}
	if memBytes > 0 {
		os.WriteFile(filepath.Join(dir, "memory.max"), []byte(fmt.Sprintf("%d", memBytes)), 0644)
	}
	if cpuMax != "" {
		os.WriteFile(filepath.Join(dir, "cpu.max"), []byte(cpuMax), 0644)
	}
	if err := os.WriteFile(filepath.Join(dir, "cgroup.procs"), []byte(fmt.Sprintf("%d", pid)), 0644); err != nil {
		return dir, err
	}
	return dir, nil
}
func child() {
	if len(os.Args) < 8 {
		fmt.Fprintln(os.Stderr, "iç hata: child argümanları eksik")
		os.Exit(1)
	}
	_ = os.Args[2]
	lower := os.Args[3]
	upper := os.Args[4]
	work := os.Args[5]
	rootfs := os.Args[6]
	command := os.Args[7]
	commandArgs := os.Args[8:]

	runDir := filepath.Dir(rootfs)
	os.WriteFile(filepath.Join(runDir, "ready"), []byte("ready"), 0644)
	if !waitForFile(filepath.Join(runDir, "go"), 5) {
		fmt.Fprintln(os.Stderr, "uyari: network barrier zaman asimi, netsiz devam")
	}
	if err := syscall.Setgid(0); err != nil {
		fmt.Fprintf(os.Stderr, "uyarı: setgid(0) başarısız: %v\n", err)
	}
	if err := syscall.Setuid(0); err != nil {
		fmt.Fprintf(os.Stderr, "uyarı: setuid(0) başarısız: %v\n", err)
	}
	extraEnv := []string{}
	if b, err := os.ReadFile(filepath.Join(runDir, "env")); err == nil {
		for _, l := range splitLinesCR(string(b)) {
			if l != "" {
				extraEnv = append(extraEnv, l)
			}
		}
	}

	defer func() {
		syscall.Unmount(filepath.Join(rootfs, "proc"), syscall.MNT_DETACH)
		syscall.Unmount(rootfs, syscall.MNT_DETACH)
	}()

	var stM, stR syscall.Stat_t
	syscall.Stat(rootfs, &stM)
	syscall.Stat(filepath.Dir(rootfs), &stR)
	if stM.Dev != stR.Dev {
		fmt.Fprintln(os.Stderr, "tekir: overlay parent'ta hazır")
	} else {
		opts := fmt.Sprintf("lowerdir=%s,upperdir=%s,workdir=%s,userxattr", lower, upper, work)
		if err := syscall.Mount("overlay", rootfs, "overlay", 0, opts); err != nil {
			panic(fmt.Errorf("overlay kurulamadı: %w", err))
		}
	}

	if err := syscall.Mount("", "/", "", syscall.MS_PRIVATE|syscall.MS_REC, ""); err != nil {
		fmt.Fprintf(os.Stderr, "uyarı:root mount private yapılamadı: %v\n", err)
	}

	if err := syscall.Mount(rootfs, rootfs, "", syscall.MS_BIND|syscall.MS_REC, ""); err != nil {
		panic(fmt.Errorf("rootfs bind edilemedi: %w", err))
	}
	if err := os.Chdir(rootfs); err != nil {
		panic(fmt.Errorf("rootfs dizinine girilemedi: %w", err))
	}
	if err := os.MkdirAll(".pivot_old", 0755); err != nil {
		panic(fmt.Errorf("pivot_old yaratılamadı: %w", err))
	}

	if err := syscall.PivotRoot(".", ".pivot_old"); err != nil {
		fmt.Fprintf(os.Stderr, "uyarı: pivot_root başarısız, chroot'a düşülüyor: %v\n", err)
		if err := syscall.Chroot("."); err != nil {
			panic(fmt.Errorf("chroot başarısız: %w", err))
		}
	} else {
		if err := os.Chdir("/"); err != nil {
			panic(fmt.Errorf("yeni root'a geçilemedi: %w", err))
		}
		if err := syscall.Unmount("/.pivot_old", syscall.MNT_DETACH); err != nil {
			fmt.Fprintf(os.Stderr, "uyarı: oldroot umount edilemedi: %v\n", err)
		}
		os.RemoveAll("/.pivot_old")
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
		dev  int
	}{
		{"/dev/null", 0666, 0x103},
		{"/dev/zero", 0666, 0x105},
		{"/dev/full", 0666, 0x107},
		{"/dev/random", 0666, 0x108},
		{"/dev/urandom", 0666, 0x109},
		{"/dev/tty", 0666, 0x500},
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
	containerEnv = append(containerEnv, extraEnv...)

	argv := append([]string{command}, commandArgs...)

	dropPrivs()

	if err := syscall.Exec(command, argv, containerEnv); err != nil {
		fmt.Println("Hata:", err)
		os.Exit(1)
	}
}

func dropPrivs() {
	const (
		SYS_PRCTL           = 157
		SYS_CAPSET          = 126
		PR_SET_NO_NEW_PRIVS = 38
		PR_CAPBSET_DROP     = 24
		CAP_VERSION_3       = 0x20080522
	)
	_, _, errno := syscall.Syscall(SYS_PRCTL, PR_SET_NO_NEW_PRIVS, 1, 0)
	if errno != 0 {
		fmt.Fprintf(os.Stderr, "uyarı: no-new-privs konulamadı: %v\n", errno)
	}
	for c := uintptr(0); c < 64; c++ {

		syscall.Syscall(SYS_PRCTL, PR_CAPBSET_DROP, c, 0)
	}
	type capHeader struct {
		version uint32
		pid     int32
	}
	type capData struct {
		effective   uint32
		permitted   uint32
		inheritable uint32
	}
	hdr := capHeader{version: CAP_VERSION_3, pid: 0}
	var data [2]capData
	_, _, errno = syscall.Syscall(
		SYS_CAPSET,
		uintptr(unsafe.Pointer(&hdr)),
		uintptr(unsafe.Pointer(&data[0])),
		0,
	)
	if errno != 0 {
		fmt.Fprintf(os.Stderr, "uyarı: capset başarısız: %v\n", errno)
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
