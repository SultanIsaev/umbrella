# Не для поставки — эфемерный образ только для make systemd-verify: реальный
# systemd как PID 1, чтобы проверить, что deploy/systemd/umbrella-collector.service
# действительно стартует (не только валиден по синтаксису), раз на macOS
# своего systemd нет и проверить юнит иначе негде.
FROM debian:12-slim
RUN apt-get update \
	&& apt-get install -y --no-install-recommends systemd systemd-sysv \
	&& rm -rf /var/lib/apt/lists/*
STOPSIGNAL SIGRTMIN+3
CMD ["/lib/systemd/systemd"]
