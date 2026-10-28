#!/usr/bin/env python3
"""Windows 11 test VM for trout, VM 9907 in the trout-test pool, unattended.

Builds an answer-file ISO from dev/windows/autounattend.xml (the password
from tmp/windows.env, the key tmp/pve_ed25519.pub), uploads it, makes a
UEFI + Secure Boot + TPM 2.0 VM with a SATA disk and an e1000 NIC (no
drivers needed), boots the Windows ISO and presses Enter through its "press
any key", then waits until ssh answers as trout. Prints the VM's address.

    dev/proxmox/pve_windows.py install [windows.iso volid]
    dev/proxmox/pve_windows.py ip
    dev/proxmox/pve_windows.py destroy
"""
import os
import shutil
import subprocess
import sys
import tempfile
import time

sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))
from pve import NODE, POOL, PVE, TMP, env  # noqa: E402
from pve_templates import KEY, SSH, find_ip  # noqa: E402

VMID = 9907
UNATTEND = "trout-unattend.iso"
HERE = os.path.dirname(os.path.abspath(__file__))


def build_iso():
    password = next(l.split("=", 1)[1].strip() for l in open(os.path.join(TMP, "windows.env")) if l.startswith("WIN_PASSWORD="))
    key = open(KEY + ".pub").read().strip()
    xml = open(os.path.join(HERE, "..", "windows", "autounattend.xml")).read()
    xml = xml.replace("@PASSWORD@", password).replace("@SSHKEY@", key)
    d = tempfile.mkdtemp()
    src = os.path.join(d, "src")
    os.mkdir(src)
    open(os.path.join(src, "autounattend.xml"), "w").write(xml)
    out = os.path.join(d, UNATTEND)
    subprocess.run(["hdiutil", "makehybrid", "-quiet", "-iso", "-joliet", "-default-volume-name", "UNATTEND",
                    "-o", out, src], check=True)
    return d, out


def upload(p, path):
    """Uploads an ISO to the node's local storage (root: the token can't)."""
    cookie, csrf = p.headers["Cookie"], p.headers["CSRFPreventionToken"]
    r = subprocess.run(["curl", "-sk", "-H", "Cookie: " + cookie, "-H", "CSRFPreventionToken: " + csrf,
                        "-F", "content=iso", "-F", "filename=@" + path + ";filename=" + UNATTEND,
                        f"https://{p.host}:8006/api2/json/nodes/{NODE}/storage/local/upload"],
                       capture_output=True, text=True, check=True)
    if '"data":"UPID' not in r.stdout:
        raise SystemExit("upload failed: " + r.stdout + r.stderr)
    p.wait(r.stdout.split('"data":"')[1].split('"')[0])


def install(iso):
    p = PVE(root=True)
    if any(v["vmid"] == VMID for v in p.get("/cluster/resources", type="vm")):
        raise SystemExit(f"VM {VMID} exists; destroy it first")
    d, path = build_iso()
    try:
        upload(p, path)
    finally:
        shutil.rmtree(d)
    print("answer file uploaded")
    p.wait(p.post(f"/nodes/{NODE}/qemu", vmid=VMID, name="trout-win11", pool=POOL,
              ostype="win11", machine="q35", bios="ovmf", cpu="host", cores=4, memory=6144,
              efidisk0="local-zfs:1,efitype=4m,pre-enrolled-keys=1", tpmstate0="local-zfs:1,version=v2.0",
              sata0="local-zfs:64,discard=on,ssd=1", net0="e1000,bridge=vmbr0",
              ide2=f"{iso},media=cdrom", ide0=f"local:iso/{UNATTEND},media=cdrom",
              boot="order=sata0;ide2", description="trout's Windows 11 test VM (made by Claude)"))
    print("VM created")
    p.wait(p.post(f"/nodes/{NODE}/qemu/{VMID}/status/start"))
    for _ in range(25):  # "Press any key to boot from CD or DVD"
        p.put(f"/nodes/{NODE}/qemu/{VMID}/sendkey", key="ret")
        time.sleep(1)
    print("Windows Setup running; waiting for ssh (20-40 minutes)")
    mac = p.get(f"/nodes/{NODE}/qemu/{VMID}/config")["net0"].split(",")[0].split("=")[1]
    return wait_ssh(mac)


def wait_ssh(mac, timeout=3600):
    end = time.time() + timeout
    while time.time() < end:
        try:
            ip = find_ip(mac, timeout=60)
        except SystemExit:
            continue
        r = subprocess.run(SSH + [f"trout@{ip}", "$PSVersionTable.PSVersion.ToString()"], capture_output=True, text=True)
        if r.returncode == 0:
            print("ssh works: PowerShell", r.stdout.strip())
            return ip
        time.sleep(20)
    raise SystemExit("no ssh after an hour")


if __name__ == "__main__":
    cmd = sys.argv[1] if len(sys.argv) > 1 else ""
    if cmd == "install":
        print(install(sys.argv[2] if len(sys.argv) > 2 else "local:iso/Windows11_Client_x64_en-us_26300_9457.iso"))
    elif cmd == "ip":
        p = PVE(root=True)
        mac = p.get(f"/nodes/{NODE}/qemu/{VMID}/config")["net0"].split(",")[0].split("=")[1]
        print(find_ip(mac))
    elif cmd == "destroy":
        p = PVE(root=True)
        vm = p.ours(VMID)
        path = f"/nodes/{NODE}/qemu/{vm}"
        if p.get(f"{path}/status/current")["status"] != "stopped":
            p.wait(p.post(f"{path}/status/stop"))
        p.wait(p.delete(path, purge=1, **{"destroy-unreferenced-disks": 1}))
        print("destroyed")
    else:
        raise SystemExit(__doc__)
