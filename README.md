# Tekir

Tekir is a docker-like container runtime that runs initramfs images in isolated Linux namespaces without VM/Virtualization.

---

## Usage:
```bash
sudo ./main run <initramfs.cpio.gz> <komut> [argümanlar...]

```

## For example:
```bash
sudo ./main run initramfs.cpio.gz /bin/sh

```
---

## Compile:
```bash
git clone https://github.com/mehmetdemir-tr/Tekir
go build -o main main.go
```
---

### LICENSE:
This program is licensed under [GPL v3.](https://github.com/mehmetdemir-tr/Tekir/blob/master/LICENSE)
