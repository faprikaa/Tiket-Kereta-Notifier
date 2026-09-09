FROM python:3.12-slim-bookworm

ENV PYTHONDONTWRITEBYTECODE=1 \
    PYTHONUNBUFFERED=1 \
    PIP_NO_CACHE_DIR=1 \
    HOME=/home/notifier \
    TZ=Asia/Jakarta

WORKDIR /app

# Same verified amd64 release as scripts/setup-ubuntu.sh.
RUN test "$(dpkg --print-architecture)" = amd64 \
    && apt-get update \
    && apt-get install -y --no-install-recommends ca-certificates curl tzdata \
    && curl -fSL https://github.com/cloudflare/cloudflared/releases/download/2026.7.2/cloudflared-linux-amd64 -o /usr/local/bin/cloudflared \
    && printf '%s  %s\n' ec905ea7b7e327ff8abdde8cb64697a2152de74dbcdbf6aec9db8364eb3886cd /usr/local/bin/cloudflared | sha256sum -c - \
    && chmod 755 /usr/local/bin/cloudflared \
    && rm -rf /var/lib/apt/lists/*

COPY requirements.txt ./
RUN pip install -r requirements.txt \
    && python -m playwright install-deps firefox \
    && rm -rf /var/lib/apt/lists/* \
    && groupadd --gid 10001 notifier \
    && useradd --create-home --uid 10001 --gid 10001 notifier

COPY main.py cloudflared.yml ./
COPY notifier/ ./notifier/

# cloudflared logs land here (bind-mount ./logs to read them from the host).
RUN mkdir -p /app/logs && chown notifier:notifier /app/logs

USER notifier
# Fetch as the runtime user so the fallback finds its browser in the same cache.
RUN python -m camoufox fetch

CMD ["python", "main.py", "-c", "/app/config.yml"]
