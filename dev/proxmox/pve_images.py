# The cloud images trout's install tests boot, by template VM ID. Each is
# downloaded by the Proxmox node itself and checked against the publisher's sums.
IMAGES = {
    9901: ("debian-12", "https://cloud.debian.org/images/cloud/bookworm/latest/debian-12-genericcloud-amd64.qcow2", "SHA512SUMS", "sha512"),
    9902: ("debian-13", "https://cloud.debian.org/images/cloud/trixie/latest/debian-13-genericcloud-amd64.qcow2", "SHA512SUMS", "sha512"),
    9903: ("ubuntu-2404", "https://cloud-images.ubuntu.com/noble/current/noble-server-cloudimg-amd64.img", "SHA256SUMS", "sha256"),
    9904: ("ubuntu-2604", "https://cloud-images.ubuntu.com/resolute/current/resolute-server-cloudimg-amd64.img", "SHA256SUMS", "sha256"),
    9905: ("rocky-9", "https://dl.rockylinux.org/pub/rocky/9/images/x86_64/Rocky-9-GenericCloud-Base.latest.x86_64.qcow2", "CHECKSUM", "sha256"),
    9906: ("rocky-10", "https://dl.rockylinux.org/pub/rocky/10/images/x86_64/Rocky-10-GenericCloud-Base.latest.x86_64.qcow2", "CHECKSUM", "sha256"),
}
