-- Схема, которую ожидает internal/storage/clickhouse.Storage (см.
-- Config.Table doc-комментарий): timestamp/source как есть, Fields —
-- JSON-строка, не нативный JSON-тип ClickHouse (чтобы не завязываться на
-- версию сервера с соответствующей поддержкой).
--
-- Выполняется автоматически при первом запуске контейнера (файлы из
-- /docker-entrypoint-initdb.d исполняются один раз, при пустом
-- /var/lib/clickhouse) — база umbrella к этому моменту уже создана самим
-- образом из переменной CLICKHOUSE_DB.
CREATE TABLE IF NOT EXISTS umbrella.events
(
    timestamp DateTime,
    source    String,
    fields    String
)
ENGINE = MergeTree
ORDER BY timestamp;
