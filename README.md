# Tekir

Tekir is a docker-like container runtime that runs initramfs images in isolated Linux namespaces without VM/Virtualization.

---

## Usage:
```bash
sudo ./tekir run <initramfs image or an .iso file> <komut> [argümanlar...]

```

## For example:
```bash
sudo ./tekir run initramfs.cpio.gz /bin/sh
sudo ./tekir run debian.iso /bin/sh
```
---

## Compile:
```bash
git clone https://github.com/mehmetdemir-tr/Tekir
go build -o tekir main.go
```
---

### LICENSE:
This program is licensed under [GPL v3.](https://github.com/mehmetdemir-tr/Tekir/blob/master/LICENSE)
