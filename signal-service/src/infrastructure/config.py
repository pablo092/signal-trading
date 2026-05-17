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

    # Observability
    jaeger_endpoint: str = Field("", alias="JAEGER_ENDPOINT")
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
