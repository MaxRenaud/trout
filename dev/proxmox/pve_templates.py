#!/usr/bin/env python3
"""Builds trout's test templates on PVE_NODE, once per image (pve_images.py).

For each: a VM from the cloud image (root: import-from takes a path), the
`tester` user with tmp/pve_ed25519.pub through cloud-init, booted once to
install the QEMU guest agent (so clones report their IP), then cleaned
(cloud-init state, machine-id: clones get their own DHCP lease and host
keys) and turned into a template. Already-built templates are skipped.

    dev/proxmox/pve_templates.py [name...]
"""
import ipaddress
import os
import subprocess
import sys
import time
import urllib.parse

sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))
from pve import NODE, POOL, PVE, TMP, env  # noqa: E402
from pve_images import IMAGES  # noqa: E402

KEY = os.path.join(TMP, "pve_ed25519")
SSH = ["ssh", "-i", KEY, "-o", "StrictHostKeyChecking=no", "-o", "UserKnownHostsFile=/dev/null",
       "-o", "LogLevel=ERROR", "-o", "ConnectTimeout=5", "-o", "BatchMode=yes"]


def find_ip(mac, timeout=300):
    """Finds a VM on the LAN (this computer's too) by its MAC, before it has the agent."""
    mac = mac.lower()
    end = time.time() + timeout
    while time.time() < end:
        # Ping the LAN (PVE_LAN) so the ARP table fills, then look the MAC up.
        lan = ipaddress.ip_network(env()["PVE_LAN"])
        subprocess.run(" ".join(f"ping -c1 -t1 {ip} >/dev/null 2>&1 &" for ip in lan.hosts()) + " wait",
                       shell=True)
        for line in subprocess.run(["arp", "-an"], capture_output=True, text=True).stdout.splitlines():
            parts = line.split()
            if len(parts) > 3 and norm(parts[3]) == mac:
                return parts[1].strip("()")
        time.sleep(5)
    raise SystemExit("no IP for " + mac)


def norm(mac):
    # macOS prints MACs without leading zeros (b0:41:6f:c:a6:29).
    try:
        return ":".join(f"{int(x, 16):02x}" for x in mac.split(":"))
    except ValueError:
        return ""


def ssh(ip, cmd, check=True):
    r = subprocess.run(SSH + [f"tester@{ip}", cmd], capture_output=True, text=True)
    if check and r.returncode:
        raise SystemExit(f"ssh {ip} {cmd!r}: {r.returncode}\n{r.stdout}{r.stderr}")
    return r


def wait_ssh(ip, timeout=300):
    end = time.time() + timeout
    while time.time() < end:
        # sudo: as a plain user, Rocky's cloud-init can't read its state and
        # --wait waits forever.
        if ssh(ip, "sudo timeout 600 cloud-init status --wait >/dev/null 2>&1; true", check=False).returncode == 0:
            return
        time.sleep(5)
    raise SystemExit("no ssh on " + ip)


def build(p, vmid, name):
    path = f"/nodes/{NODE}/qemu/{vmid}"
    existing = {v["vmid"]: v for v in p.get("/cluster/resources", type="vm")}
    if vmid in existing:
        if existing[vmid].get("pool") != POOL:
            raise SystemExit(f"{vmid} is someone else's VM")
        if existing[vmid].get("template"):
            print(name, "template already built")
            return
        raise SystemExit(f"{vmid} ({name}) exists but isn't a template; delete it and rerun")
    key = open(KEY + ".pub").read().strip()
    print(name, "creating", vmid)
    p.wait(p.post(f"/nodes/{NODE}/qemu", vmid=vmid, name=f"trout-tpl-{name}", pool=POOL,
                  memory=2048, cores=2, cpu="host", ostype="l26", agent="enabled=1",
                  scsihw="virtio-scsi-single",
                  scsi0=f"local-zfs:0,import-from=/var/lib/vz/template/iso/trout-{name}.img,discard=on,ssd=1",
                  ide2="local-zfs:cloudinit", boot="order=scsi0", serial0="socket", vga="serial0",
                  net0="virtio,bridge=vmbr0", ciuser="tester", ipconfig0="ip=dhcp",
                  sshkeys=urllib.parse.quote(key + "\n", safe=""),
                  description="trout bouncer-install test template (made by Claude)"))
    r = p.put(f"{path}/resize", disk="scsi0", size="10G")
    if isinstance(r, str) and r.startswith("UPID"):  # a task on newer Proxmox
        p.wait(r)
    p.wait(p.post(f"{path}/status/start"))
    mac = p.get(f"{path}/config")["net0"].split(",")[0].split("=")[1]
    ip = find_ip(mac)
    print(name, "up at", ip)
    wait_ssh(ip)
    install = ("if command -v apt-get >/dev/null; then sudo DEBIAN_FRONTEND=noninteractive apt-get -qq update && "
               "sudo DEBIAN_FRONTEND=noninteractive apt-get -qq install -y qemu-guest-agent; "
               "else sudo dnf -q -y install qemu-guest-agent; fi && sudo systemctl enable qemu-guest-agent")
    ssh(ip, install)
    # Clones must not share machine-id (DHCP client ID, journald) or cloud-init state.
    ssh(ip, "sudo cloud-init clean --logs --seed; sudo truncate -s0 /etc/machine-id; "
            "sudo rm -f /var/lib/dbus/machine-id; sudo sync; (sleep 1; sudo poweroff) >/dev/null 2>&1 &", check=False)
    for _ in range(90):
        if p.get(f"{path}/status/current")["status"] == "stopped":
            break
        time.sleep(2)
    else:
        raise SystemExit(name + " didn't power off")
    p.post(f"{path}/template")
    print(name, "template ready")


if __name__ == "__main__":
    want = set(sys.argv[1:])
    p = PVE(root=True)
    for vmid, (name, *_rest) in IMAGES.items():
        if not want or name in want:
            build(p, vmid, name)
