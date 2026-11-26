# One image, one entry point: the exporter. The static site is optional and can
# be served from the same image with `--entrypoint python -m hitron.site`.
FROM python:3.12-slim

ENV PYTHONDONTWRITEBYTECODE=1 \
    PYTHONUNBUFFERED=1 \
    PIP_NO_CACHE_DIR=1

WORKDIR /opt/hitron

COPY pyproject.toml README.md ./
COPY src ./src

RUN pip install --no-cache-dir . && \
    addgroup --system hitron && adduser --system --ingroup hitron hitron

USER hitron
EXPOSE 9877
HEALTHCHECK --interval=30s --timeout=10s --start-period=20s --retries=3 \
  CMD python -c "import urllib.request;urllib.request.urlopen('http://127.0.0.1:9877/healthz',timeout=5)"

ENTRYPOINT ["hitron-exporter"]
