# ── Builder stage ────────────────────────────────────────────────────────────
FROM python:3.11-slim AS builder

WORKDIR /build

# Runtime dependencies only, pinned with hashes (generated from pyproject.toml:
#   uv pip compile pyproject.toml --universal --python-version 3.11 --generate-hashes -o requirements.lock)
COPY requirements.lock ./

RUN pip install --no-cache-dir --require-hashes --prefix=/install -r requirements.lock

# ── Runtime stage ─────────────────────────────────────────────────────────────
FROM python:3.11-slim AS runtime

# Non-root user for security
RUN useradd --create-home --shell /bin/bash appuser
WORKDIR /app

# Copy installed packages from builder
COPY --from=builder /install /usr/local

# Copy application source
COPY --chown=appuser:appuser src/ ./src/

USER appuser

ENV PYTHONPATH=/app \
    PYTHONDONTWRITEBYTECODE=1 \
    PYTHONUNBUFFERED=1 \
    PORT=8000

EXPOSE 8000

HEALTHCHECK --interval=30s --timeout=5s --start-period=10s --retries=3 \
    CMD python -c "import httpx; httpx.get('http://localhost:8000/health/ready').raise_for_status()"

CMD ["uvicorn", "--factory", "src.main:create_app", "--host", "0.0.0.0", "--port", "8000", "--workers", "2"]
