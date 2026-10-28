#!/usr/bin/env python3
"""Throwaway test VMs from trout's templates, with the trout@pve token only.

    dev/proxmox/pve_vm.py up debian-12      linked clone, boot, print "vmid ip"
    dev/proxmox/pve_vm.py ssh 9910 [cmd]    as tester, with tmp/pve_ed25519
    dev/proxmox/pve_vm.py down 9910         stop and destroy
    dev/proxmox/pve_vm.py list              trout's VMs

Clones get IDs 9910-9999 in the trout-test pool; down refuses anything else.
"""
import os
import random
import subprocess
import sys
import time

sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))
from pve import NODE, POOL, PVE  # noqa: E402
from pve_images import IMAGES  # noqa: E402
from pve_templates import SSH  # noqa: E402

CLONES = range(9910, 10000)


def ip_of(p, vmid, timeout=240):
    """The VM's IPv4 on the LAN, from the guest agent."""
    end = time.time() + timeout
    while time.time() < end:
        try:
            ifs = p.get(f"/nodes/{NODE}/qemu/{vmid}/agent/network-get-interfaces")["result"]
        except SystemExit:  # the agent isn't up yet
            ifs = []
        for i in ifs:
            for a in i.get("ip-addresses", []):
                if a["ip-address-type"] == "ipv4" and not a["ip-address"].startswith("127."):
                    return a["ip-address"]
        time.sleep(3)
    raise SystemExit(f"{vmid}: no IP from the guest agent")


def up(p, name):
    tpl = next((vmid for vmid, (n, *_r) in IMAGES.items() if n == name), None)
    if tpl is None:
        raise SystemExit(f"no template {name}; have {', '.join(n for n, *_r in IMAGES.values())}")
    # Several tests clone at once: start from a random free ID, and if
    # another clone took it first (it exists, or is locked), try another.
    for _ in range(10):
        used = {v["vmid"] for v in p.get("/cluster/resources", type="vm")}
        free = [i for i in CLONES if i not in used]
        if not free:
            raise SystemExit("no free VM ID for a clone")
        vmid = random.choice(free)
        try:
            p.wait(p.post(f"/nodes/{NODE}/qemu/{tpl}/clone", newid=vmid, name=f"trout-test-{name}-{vmid}",
                          pool=POOL, full=0))
            break
        except SystemExit as e:
            if not any(m in str(e) for m in ("already exists", "unable to create VM", "can't lock file")):
                raise
    else:
        raise SystemExit("couldn't get a VM ID for a clone")
    p.wait(p.post(f"/nodes/{NODE}/qemu/{vmid}/status/start"))
    ip = ip_of(p, vmid)
    end = time.time() + 180
    while subprocess.run(SSH + [f"tester@{ip}", "true"], capture_output=True).returncode:
        if time.time() > end:
            raise SystemExit(f"{vmid}: no ssh on {ip}")
        time.sleep(3)
    return vmid, ip


def down(p, vmid):
    vmid = p.ours(vmid)
    if vmid not in CLONES:
        raise SystemExit(f"{vmid} is a template, not a test VM")
    path = f"/nodes/{NODE}/qemu/{vmid}"
    if p.get(f"{path}/status/current")["status"] != "stopped":
        p.wait(p.post(f"{path}/status/stop"))
    p.wait(p.delete(path, purge=1, **{"destroy-unreferenced-disks": 1}))


if __name__ == "__main__":
    p = PVE()
    cmd, args = sys.argv[1], sys.argv[2:]
    if cmd == "up":
        print(*up(p, args[0]))
    elif cmd == "down":
        for a in args:
            down(p, a)
    elif cmd == "ssh":
        vmid = p.ours(args[0])
        os.execvp("ssh", SSH + [f"tester@{ip_of(p, vmid)}"] + args[1:])
    elif cmd == "list":
        for v in sorted(p.get("/cluster/resources", type="vm"), key=lambda v: v["vmid"]):
            print(v["vmid"], v["name"], "template" if v.get("template") else v["status"])
    else:
        raise SystemExit(__doc__)
