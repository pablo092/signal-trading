"""Application settings loaded from environment variables via Pydantic BaseSettings."""

from __future__ import annotations

from pydantic import Field
from pydantic_settings import BaseSettings, SettingsConfigDict


class Settings(BaseSettings):
    model_config = SettingsConfigDict(
        env_file=".env",
        env_file_encoding="utf-8",
        case_sensitive=False,
        extra="ignore",
    )

    # Runtime
    environment: str = Field("development", alias="ENVIRONMENT")
    port: int = Field(8000, alias="PORT")
    log_level: str = Field("INFO", alias="LOG_LEVEL")

    # Alpaca
    alpaca_api_key: str = Field(..., alias="ALPACA_API_KEY")
    alpaca_secret_key: str = Field(..., alias="ALPACA_SECRET_KEY")
    paper_trading: bool = Field(True, alias="PAPER_TRADING")
    alpaca_data_feed: str = Field("iex", alias="ALPACA_DATA_FEED")

    # Market data (TD-13)
    market_data_timeout_seconds: float = Field(10.0, gt=0, alias="MARKET_DATA_TIMEOUT_SECONDS")
    market_data_cache_ttl_seconds: float = Field(30.0, ge=0, alias="MARKET_DATA_CACHE_TTL_SECONDS")

    # Auth (ADR-003): scoped token table, same format as bot-engine. Empty = every API call is 401.
    auth_tokens: str = Field("", alias="AUTH_TOKENS", repr=False)
    auth_tokens_file: str = Field("", alias="AUTH_TOKENS_FILE")

    # Observability. Tracing is configured by the standard OTEL_EXPORTER_OTLP_*
    # variables (see src.infrastructure.observability), not by a setting here.
    metrics_enabled: bool = Field(True, alias="METRICS_ENABLED")

    # Storage
    redis_url: str = Field("redis://localhost:6379/0", alias="REDIS_URL")
    database_url: str = Field(
        "postgresql+asyncpg://postgres:postgres@localhost:5432/tradingbot",
        alias="DATABASE_URL",
    )

    @property
    def is_production(self) -> bool:
        return self.environment == "production"
