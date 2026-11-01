# trout's soju setup. trout puts its settings above this line and uploads
# it with soju, sojuctl, trout-cert and the TLS key into the same folder.
# Run it as root:
#
#   sh setup.sh          install soju, and set your account
#   sh setup.sh update   put in the soju next to this script, keeping the key
#                        and accounts (and trout-cert, if it's set up)
#   sh setup.sh cert     get a certificate phones and browsers trust from
#                        Let's Encrypt for soju's key, and renew it with a
#                        timer (trout-cert); Let's Encrypt checks port 80
#   sh setup.sh nocert   stop that and go back to the self-signed certificate
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

firewall() { # $1: allow or deny, then the ports
	action=$1
	shift
	if ufw_on; then
		for p in "$@"; do
			if [ "$action" = allow ]; then ufw allow "$p/tcp" >/dev/null; else ufw delete allow "$p/tcp" >/dev/null 2>&1 || true; fi
		done
		step "ufw: $action ports $*"
	fi
	if firewalld_on; then
		for p in "$@"; do
			if [ "$action" = allow ]; then
				firewall-cmd --quiet --permanent --add-port="$p/tcp"
			else
				firewall-cmd --quiet --permanent --remove-port="$p/tcp" 2>/dev/null || true
			fi
		done
		firewall-cmd --quiet --reload
		step "firewalld: $action ports $*"
	fi
}
ufw_on() { command -v ufw >/dev/null 2>&1 && ufw status 2>/dev/null | grep -q "^Status: active"; }
firewalld_on() { command -v firewall-cmd >/dev/null 2>&1 && firewall-cmd --state >/dev/null 2>&1; }

# Port 80 is Let's Encrypt's, for trout-cert. It's only closed again if
# trout opened it (noted in port80), not when it was open already.
port80=/var/lib/trout-cert/port80
open80() {
	opened=
	if ufw_on && ! ufw status | grep -Eq "^80(/tcp)?[[:space:]]+ALLOW"; then
		ufw allow 80/tcp >/dev/null
		opened="$opened ufw"
	fi
	if firewalld_on && ! firewall-cmd --permanent --query-port=80/tcp >/dev/null 2>&1 &&
		! firewall-cmd --permanent --query-service=http >/dev/null 2>&1; then
		firewall-cmd --quiet --permanent --add-port=80/tcp
		firewall-cmd --quiet --reload
		opened="$opened firewalld"
	fi
	if [ -n "$opened" ]; then
		echo "$opened" >"$port80"
		step "Opened port 80 for Let's Encrypt in$opened"
	fi
}
close80() {
	[ -f "$port80" ] || return 0
	if grep -q ufw "$port80" && command -v ufw >/dev/null 2>&1; then ufw delete allow 80/tcp >/dev/null 2>&1 || true; fi
	if grep -q firewalld "$port80" && firewalld_on; then
		firewall-cmd --quiet --permanent --remove-port=80/tcp 2>/dev/null || true
		firewall-cmd --quiet --reload
	fi
	step "Closed port 80 again"
}

# trout-cert's timer runs it twice a day; it renews once half the
# certificate's time is up (about 3 days for an IP address's).
cert_units() {
	cat >/etc/systemd/system/trout-cert.service <<EOF
# Written by trout's setup.
[Unit]
Description=Renew the bouncer's Let's Encrypt certificate when it's due (set up by trout)
Wants=network-online.target
After=network-online.target soju.service

[Service]
Type=oneshot
ExecStart=/usr/local/bin/trout-cert renew -host $TROUT_HOST -acme $TROUT_ACME
NoNewPrivileges=yes
PrivateTmp=yes
ProtectHome=yes
ProtectSystem=strict
ReadWritePaths=/etc/soju /var/lib/trout-cert
EOF
	cat >/etc/systemd/system/trout-cert.timer <<'EOF'
# Written by trout's setup.
[Unit]
Description=Renew the bouncer's certificate twice a day when it's due (set up by trout)

[Timer]
OnCalendar=*-*-* 00,12:00:00
RandomizedDelaySec=4h
Persistent=true

[Install]
WantedBy=timers.target
EOF
	command -v restorecon >/dev/null 2>&1 && restorecon /usr/local/bin/trout-cert /etc/systemd/system/trout-cert.service /etc/systemd/system/trout-cert.timer
	systemctl daemon-reload
}

cert_remove() {
	systemctl disable --now trout-cert.timer >/dev/null 2>&1 || true
	rm -f /etc/systemd/system/trout-cert.service /etc/systemd/system/trout-cert.timer /usr/local/bin/trout-cert
	systemctl daemon-reload
	close80
	rm -rf /var/lib/trout-cert
}

case $mode in
remove | purge)
	step "Stopping soju"
	systemctl disable --now soju >/dev/null 2>&1 || true
	rm -f /etc/systemd/system/soju.service /usr/local/bin/soju /usr/local/bin/sojuctl
	systemctl daemon-reload
	firewall deny 6697 8443
	cert_remove
	rm -rf /etc/soju
	if [ "$mode" = purge ]; then
		step "Deleting soju's history"
		rm -rf /var/lib/soju
		userdel soju 2>/dev/null || true
	fi
	step "done"
	exit 0
	;;
cert)
	[ -x /usr/local/bin/soju ] && [ -f /etc/soju/tls.key ] || fail "soju isn't set up on this server: set it up first"
	step "Installing trout-cert"
	install -m 0755 -o root -g root "$here/trout-cert" /usr/local/bin/
	install -d -m 0700 /var/lib/trout-cert
	cert_units
	open80
	# It says its own steps, and why if it fails.
	/usr/local/bin/trout-cert renew -host "$TROUT_HOST" -acme "$TROUT_ACME" -steps
	step "Renewing it from now on, twice a day when it's due"
	systemctl enable --now trout-cert.timer >/dev/null 2>&1
	step "done"
	exit 0
	;;
nocert)
	step "Stopping the certificate's renewal"
	systemctl disable --now trout-cert.timer >/dev/null 2>&1 || true
	if [ -x /usr/local/bin/trout-cert ]; then
		/usr/local/bin/trout-cert self-signed -host "$TROUT_HOST" -steps
	fi
	cert_remove
	step "done"
	exit 0
	;;
install | update) ;;
*) fail "unknown mode $mode (install, update, cert, nocert, remove or purge)" ;;
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
	rm -f /etc/soju/tls.crt.self-signed # trout-cert's copy, for the old key
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

if [ -x /usr/local/bin/trout-cert ] && [ -f "$here/trout-cert" ]; then
	step "Updating trout-cert"
	install -m 0755 -o root -g root "$here/trout-cert" /usr/local/bin/
	cert_units
fi

firewall allow 6697 8443

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
