# A server for trout's install test on this computer's architecture (arm64
# on Apple silicon): Debian with systemd as PID 1, sshd and sudo, and a
# `tester` user with passwordless sudo. Run it privileged with the host's
# cgroups so systemd can work (internal/install/docker_test.go does).
FROM debian:13
ENV container=docker
RUN apt-get update && DEBIAN_FRONTEND=noninteractive apt-get install -y --no-install-recommends \
        systemd systemd-sysv dbus openssh-server sudo iproute2 ca-certificates \
    && apt-get clean \
    && useradd -m -s /bin/bash tester \
    && echo 'tester ALL=(ALL) NOPASSWD:ALL' > /etc/sudoers.d/tester \
    && mkdir -p /home/tester/.ssh && chown tester:tester /home/tester/.ssh && chmod 700 /home/tester/.ssh \
    && systemctl enable ssh
STOPSIGNAL SIGRTMIN+3
CMD ["/sbin/init"]
