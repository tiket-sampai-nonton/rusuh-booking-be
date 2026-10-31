#!/usr/bin/env bash
# Menyiapkan VPS dev yang masih kosong (Ubuntu 24.04), dijalankan SEKALI sebagai root:
#   DEPLOY_PUBKEY="ssh-ed25519 AAAA... rusuh-dev-deploy" bash server-bootstrap.sh
#
# Yang dilakukan: swap 2 GB, paket dasar, user `deploy` (SSH key saja, untuk CI), UFW (SSH + 80 + 443), fail2ban,
# unattended-upgrades, Docker + Compose, dan folder /opt/rusuh.
#
# Yang SENGAJA TIDAK diubah: konfigurasi SSH. Login root dengan password dan dengan key tetap berfungsi, dan port SSH
# tetap yang sekarang (mis. 20130). Port SSH dideteksi dan ikut dibuka di UFW agar tidak terkunci.
# Cadangan keamanan untuk login password: fail2ban (ban 1 jam setelah 5 kegagalan). Gunakan password yang panjang dan unik.
set -euo pipefail
[ "$(id -u)" = 0 ] || { echo "jalankan sebagai root" >&2; exit 1; }
: "${DEPLOY_PUBKEY:?isi DEPLOY_PUBKEY dengan isi file *.pub kunci deploy (bukan private key)}"
case "$DEPLOY_PUBKEY" in ssh-ed25519\ *|ssh-rsa\ *|ecdsa-*) ;; *) echo "DEPLOY_PUBKEY bukan public key SSH" >&2; exit 1;; esac

export DEBIAN_FRONTEND=noninteractive

echo "== paket dasar"
apt-get update -y
apt-get install -y ca-certificates curl ufw fail2ban unattended-upgrades gnupg

echo "== swap 2 GB (pengaman memori)"
if ! swapon --show | grep -q .; then
  fallocate -l 2G /swapfile && chmod 600 /swapfile && mkswap /swapfile && swapon /swapfile
  grep -q '^/swapfile' /etc/fstab || echo '/swapfile none swap sw 0 0' >> /etc/fstab
fi

echo "== user deploy (login hanya dengan SSH key)"
id deploy >/dev/null 2>&1 || adduser --disabled-password --gecos "" deploy
install -d -m 700 -o deploy -g deploy /home/deploy/.ssh
touch /home/deploy/.ssh/authorized_keys
grep -qxF "$DEPLOY_PUBKEY" /home/deploy/.ssh/authorized_keys || echo "$DEPLOY_PUBKEY" >> /home/deploy/.ssh/authorized_keys
chown deploy:deploy /home/deploy/.ssh/authorized_keys && chmod 600 /home/deploy/.ssh/authorized_keys

echo "== firewall (UFW): SSH + 80 + 443"
# Port SSH: dari konfigurasi sshd, dari koneksi yang sedang dipakai, dan 22 sebagai cadangan; semuanya dibuka agar tidak terkunci.
ssh_ports="$( { sshd -T 2>/dev/null | awk '/^port /{print $2}'; [ -n "${SSH_CONNECTION:-}" ] && echo "${SSH_CONNECTION##* }"; echo 22; } | sort -un )"
echo "port SSH yang dibuka: $(echo $ssh_ports)"
ufw default deny incoming
ufw default allow outgoing
for p in $ssh_ports; do ufw allow "$p"/tcp; done
ufw allow 80/tcp
ufw allow 443/tcp
ufw --force enable

echo "== fail2ban untuk SSH"
primary="$(echo "$ssh_ports" | grep -v '^22$' | head -1)"; primary="${primary:-22}"
cat > /etc/fail2ban/jail.d/rusuh.local <<JAIL
[sshd]
enabled  = true
port     = $(echo $ssh_ports | tr ' ' ',')
maxretry = 5
findtime = 10m
bantime  = 1h
JAIL
systemctl enable --now fail2ban
systemctl restart fail2ban

echo "== pembaruan keamanan otomatis"
dpkg-reconfigure -f noninteractive unattended-upgrades || true

echo "== Docker + Compose"
if ! command -v docker >/dev/null 2>&1; then curl -fsSL https://get.docker.com | sh; fi
usermod -aG docker deploy
systemctl enable --now docker

echo "== folder aplikasi"
install -d -m 750 -o deploy -g deploy /opt/rusuh /opt/rusuh/repo

echo
echo "SELESAI. Ringkasan:"
uname -m; . /etc/os-release && echo "$PRETTY_NAME"
docker --version; docker compose version
ufw status | head -12
echo "Uji login deploy dari laptop (terminal lain, jangan tutup sesi ini):"
echo "  ssh -p $primary -i ~/.ssh/rusuh_dev_deploy deploy@$(hostname -I | awk '{print $1}')   # ganti IP publik bila berbeda"
