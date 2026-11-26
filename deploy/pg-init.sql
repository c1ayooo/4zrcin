-- postgres 容器首次初始化时执行（挂到 docker-entrypoint-initdb.d）。
-- docs/16 §11：PostgreSQL 16+，全文检索用 pg_trgm。
CREATE EXTENSION IF NOT EXISTS pg_trgm;
