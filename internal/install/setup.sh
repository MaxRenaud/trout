# trout's soju setup. trout puts its settings above this line and uploads
# it with soju, sojuctl and the TLS key into the same folder. Run it as root:
#
#   sh setup.sh          install soju, and set your account
#   sh setup.sh update   put in the soju next to this script, keeping the key
#                        and accounts
#   sh setup.sh remove   stop and remove soju, keeping its history
#   sh setup.sh purge    remove soju and its history
#
# Running it again is safe: every step checks what's already there.
set -eu

step() { echo "trout-step: $*"; }
fail() { echo "trout-fail: $*"; exit 1; }

[ "$(id -u)" = 0 ] || fail "run this as root: sudo sh $0"
command -v systemctl >/dev/null 2>&1 && [ -d /run/systemd/system ] || fail "this server doesn't run systemd"
here=$(cd "$(dirname "$0")" && pwd)
mode=${1:-install}

firewall() { # $1: allow or deny
	if command -v ufw >/dev/null 2>&1 && ufw status 2>/dev/null | grep -q "^Status: active"; then
		for p in 6697 8443; do
			if [ "$1" = allow ]; then ufw allow "$p/tcp" >/dev/null; else ufw delete allow "$p/tcp" >/dev/null 2>&1 || true; fi
		done
		step "ufw: $1 ports 6697 and 8443"
	fi
	if command -v firewall-cmd >/dev/null 2>&1 && firewall-cmd --state >/dev/null 2>&1; then
		for p in 6697 8443; do
			if [ "$1" = allow ]; then
				firewall-cmd --quiet --permanent --add-port="$p/tcp"
			else
				firewall-cmd --quiet --permanent --remove-port="$p/tcp" 2>/dev/null || true
			fi
		done
		firewall-cmd --quiet --reload
		step "firewalld: $1 ports 6697 and 8443"
	fi
}

case $mode in
remove | purge)
	step "Stopping soju"
	systemctl disable --now soju >/dev/null 2>&1 || true
	rm -f /etc/systemd/system/soju.service /usr/local/bin/soju /usr/local/bin/sojuctl
	systemctl daemon-reload
	firewall deny
	rm -rf /etc/soju
	if [ "$mode" = purge ]; then
		step "Deleting soju's history"
		rm -rf /var/lib/soju
		userdel soju 2>/dev/null || true
	fi
	step "done"
	exit 0
	;;
install | update) ;;
*) fail "unknown mode $mode (install, update, remove or purge)" ;;
esac

step "Creating the soju system user"
if ! id soju >/dev/null 2>&1; then
	nologin=/usr/sbin/nologin
	[ -x "$nologin" ] || nologin=/sbin/nologin
	useradd --system --home-dir /var/lib/soju --no-create-home --shell "$nologin" soju
fi

step "Installing soju $TROUT_SOJU_VERSION"
# install(1) makes new files, so on SELinux they get /usr/local/bin's label,
# not the label of the uploaded copies in your home.
install -m 0755 -o root -g root "$here/soju" "$here/sojuctl" /usr/local/bin/
install -d -m 0750 -o root -g soju /etc/soju
if [ -f "$here/tls.key" ]; then # an update keeps the key, and so the pin
	install -m 0640 -o root -g soju "$here/tls.crt" "$here/tls.key" /etc/soju/
fi
[ -f /etc/soju/tls.key ] || fail "there's no TLS key in /etc/soju: set soju up again"
install -d -m 0750 -o soju -g soju /var/lib/soju /var/lib/soju/uploads

step "Writing /etc/soju/config"
cat >/etc/soju/config.new <<EOF
# Written by trout's setup; running it again overwrites this file.
listen ircs://:6697
listen https://:8443
listen unix+admin:///run/soju/admin
tls /etc/soju/tls.crt /etc/soju/tls.key
hostname $TROUT_HOST
http-ingress $TROUT_INGRESS
db sqlite3 /var/lib/soju/soju.db
file-upload fs /var/lib/soju/uploads
client-cert-auth true
EOF
chown root:soju /etc/soju/config.new
chmod 0640 /etc/soju/config.new
mv /etc/soju/config.new /etc/soju/config

step "Writing soju.service"
cat >/etc/systemd/system/soju.service <<'EOF'
# Written by trout's setup, after soju's contrib/soju.service.
[Unit]
Description=soju IRC bouncer (set up by trout)
Documentation=https://soju.im/
Wants=network-online.target
After=network-online.target

[Service]
Type=simple
User=soju
Group=soju
ExecStart=/usr/local/bin/soju -config /etc/soju/config
ExecReload=/bin/kill -HUP $MAINPID
Restart=on-failure
RuntimeDirectory=soju
NoNewPrivileges=yes
ProtectSystem=strict
ProtectHome=yes
PrivateTmp=yes
ReadWritePaths=/var/lib/soju

[Install]
WantedBy=multi-user.target
EOF
command -v restorecon >/dev/null 2>&1 && restorecon -R /usr/local/bin/soju /usr/local/bin/sojuctl /etc/soju /etc/systemd/system/soju.service /var/lib/soju

firewall allow

step "Starting soju"
systemctl daemon-reload
systemctl enable soju >/dev/null 2>&1
systemctl restart soju
i=0
until [ -S /run/soju/admin ]; do
	i=$((i + 1))
	[ "$i" -le 100 ] || fail "soju didn't start: $(journalctl -u soju -n 5 --no-pager 2>&1 | tail -n 5)"
	sleep 0.1
done

[ -n "$TROUT_PASSWORD" ] || { step "done"; exit 0; } # an update leaves accounts be
step "Setting up your account, $TROUT_ACCOUNT"
ctl() { /usr/local/bin/sojuctl -config /etc/soju/config "$@"; }
if ctl user status "$TROUT_ACCOUNT" >/dev/null 2>&1; then
	ctl user update "$TROUT_ACCOUNT" -password "$TROUT_PASSWORD" -admin=true >/dev/null
else
	ctl user create -username "$TROUT_ACCOUNT" -password "$TROUT_PASSWORD" -admin=true >/dev/null
fi
step "done"
