#!/usr/bin/env python3
"""Tiny Proxmox API client for trout's bouncer-install test VMs.

Reads tmp/proxmox.env (git-ignored): PVE_HOST, PVE_NODE, root's login for
setup, and the trout@pve token. Uses the trout@pve token when it's there (tests),
root@pam only with --root (one-time setup: pool, user, token, templates).
Everything trout creates lives in the trout-test pool, VM IDs 9900-9999;
never touch anything else on the cluster.

    dev/proxmox/pve.py [--root] GET /cluster/resources type=vm
    dev/proxmox/pve.py [--root] POST /pools poolid=trout-test
"""
import json
import os
import ssl
import sys
import time
import urllib.parse
import urllib.request

HERE = os.path.dirname(os.path.abspath(__file__))
TMP = os.path.join(HERE, "..", "..", "tmp")  # credentials and the test VMs' SSH key
POOL = "trout-test"
VMIDS = range(9900, 10000)


def env():
    out = {}
    with open(os.path.join(TMP, "proxmox.env")) as f:
        for line in f:
            line = line.split("#", 1)[0].strip()
            if "=" in line:
                k, v = line.split("=", 1)
                out[k] = v.strip().strip('"')
    return out


NODE = env()["PVE_NODE"]  # where trout's templates and clones live


class PVE:
    def __init__(self, root=False):
        self.ctx = ssl.create_default_context()
        self.ctx.check_hostname, self.ctx.verify_mode = False, ssl.CERT_NONE  # self-signed, LAN
        e = env()
        self.host = e["PVE_HOST"]
        self.headers = {}
        if not root and e.get("PVE_TOKEN_ID"):
            self.headers["Authorization"] = f"PVEAPIToken={e['PVE_TOKEN_ID']}={e['PVE_TOKEN_SECRET']}"
        else:
            d = self._req("POST", "/access/ticket", {"username": e["PVE_ROOT_USER"], "password": e["PVE_ROOT_PASSWORD"]})
            self.headers["Cookie"] = "PVEAuthCookie=" + d["ticket"]
            self.headers["CSRFPreventionToken"] = d["CSRFPreventionToken"]

    def _req(self, method, path, params=None):
        url = f"https://{getattr(self, 'host', None) or env()['PVE_HOST']}:8006/api2/json{path}"
        data = None
        if params and method == "GET":
            url += "?" + urllib.parse.urlencode(params)
        elif params:
            data = urllib.parse.urlencode(params).encode()
        r = urllib.request.Request(url, data=data, method=method, headers=getattr(self, "headers", {}))
        try:
            with urllib.request.urlopen(r, context=self.ctx, timeout=60) as resp:
                return json.load(resp)["data"]
        except urllib.error.HTTPError as err:
            raise SystemExit(f"{method} {path}: {err.code} {err.reason}: {err.read().decode()[:500]}")

    def get(self, _path, **p):
        return self._req("GET", _path, p)

    def post(self, _path, **p):
        return self._req("POST", _path, p)

    def put(self, _path, **p):
        return self._req("PUT", _path, p)

    def delete(self, _path, **p):
        return self._req("DELETE", _path + ("?" + urllib.parse.urlencode(p) if p else ""))

    def wait(self, upid, node=NODE, timeout=900):
        """Waits for a task (UPID) and fails if it didn't end OK."""
        end = time.time() + timeout
        while time.time() < end:
            s = self.get(f"/nodes/{node}/tasks/{urllib.parse.quote(upid)}/status")
            if s["status"] == "stopped":
                if s.get("exitstatus") != "OK":
                    log = self.get(f"/nodes/{node}/tasks/{urllib.parse.quote(upid)}/log", limit=50)
                    raise SystemExit(f"task failed: {s.get('exitstatus')}\n" + "\n".join(l["t"] for l in log))
                return
            time.sleep(2)
        raise SystemExit("task timed out: " + upid)

    def ours(self, vmid):
        """Refuses to act on a VM outside trout's pool and ID range."""
        vmid = int(vmid)
        if vmid not in VMIDS:
            raise SystemExit(f"VM {vmid} isn't in trout's range {VMIDS.start}-{VMIDS.stop - 1}")
        for v in self.get("/cluster/resources", type="vm"):
            if v["vmid"] == vmid and v.get("pool") != POOL:
                raise SystemExit(f"VM {vmid} exists outside the {POOL} pool; not touching it")
        return vmid


if __name__ == "__main__":
    args = sys.argv[1:]
    root = "--root" in args
    args = [a for a in args if a != "--root"]
    method, path, rest = args[0].upper(), args[1], args[2:]
    params = dict(a.split("=", 1) for a in rest)
    print(json.dumps(PVE(root)._req(method, path, params), indent=1))
