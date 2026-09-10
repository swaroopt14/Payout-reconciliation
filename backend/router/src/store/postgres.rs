use sqlx::postgres::PgPoolOptions;
use sqlx::{PgPool, Row};
use tokio::time::{timeout, Duration};

use crate::catalog::Processor;
use crate::routing::rules::seed_rules;
use crate::types::{Direction, Psp, Rail, RouteDecision, RoutingRule};

const MIGRATION: &str = include_str!("../../migrations/001_routing.sql");

#[derive(Clone)]
pub struct Postgres {
    pool: PgPool,
    timeout: Duration,
}

impl Postgres {
    pub async fn connect(url: &str, wait: Duration) -> Result<Self, String> {
        let pool = timeout(
            Duration::from_secs(5),
            PgPoolOptions::new().max_connections(5).connect(url),
        )
        .await
        .map_err(|_| "postgres connect timeout".to_string())?
        .map_err(|e| e.to_string())?;
        let pg = Self { pool, timeout: wait };
        pg.migrate().await?;
        pg.seed_if_empty().await?;
        Ok(pg)
    }

    async fn migrate(&self) -> Result<(), String> {
        for stmt in MIGRATION.split(';') {
            let stmt = stmt.trim();
            if stmt.is_empty() {
                continue;
            }
            sqlx::query(stmt)
                .execute(&self.pool)
                .await
                .map_err(|e| format!("migrate: {e}"))?;
        }
        Ok(())
    }

    async fn seed_if_empty(&self) -> Result<(), String> {
        let count: i64 = sqlx::query_scalar("SELECT COUNT(*) FROM routing_rules")
            .fetch_one(&self.pool)
            .await
            .map_err(|e| e.to_string())?;
        if count == 0 {
            for rule in seed_rules() {
                self.upsert_rule(&rule).await?;
            }
        }
        let pcount: i64 = sqlx::query_scalar("SELECT COUNT(*) FROM processors")
            .fetch_one(&self.pool)
            .await
            .map_err(|e| e.to_string())?;
        if pcount == 0 {
            for p in crate::catalog::processors() {
                self.upsert_processor(&p).await?;
            }
        }
        Ok(())
    }

    pub async fn ping(&self) -> bool {
        timeout(self.timeout, sqlx::query_scalar::<_, i32>("SELECT 1").fetch_one(&self.pool))
            .await
            .ok()
            .and_then(|r| r.ok())
            .is_some()
    }

    pub async fn load_rules(&self, merchant_id: Option<&str>) -> Result<Vec<RoutingRule>, String> {
        let rows = timeout(
            self.timeout,
            sqlx::query(
                "SELECT id, merchant_id, name, priority, condition, action, enabled
                 FROM routing_rules
                 WHERE enabled = TRUE
                   AND (merchant_id IS NULL OR merchant_id = $1)
                 ORDER BY priority ASC",
            )
            .bind(merchant_id)
            .fetch_all(&self.pool),
        )
        .await
        .map_err(|_| "postgres timeout".to_string())?
        .map_err(|e| e.to_string())?;
        Ok(rows
            .into_iter()
            .filter_map(|row| {
                Some(RoutingRule {
                    id: row.try_get("id").ok()?,
                    merchant_id: row.try_get("merchant_id").ok()?,
                    name: row.try_get("name").ok()?,
                    priority: row.try_get("priority").ok()?,
                    enabled: row.try_get("enabled").ok()?,
                    condition: row.try_get("condition").ok()?,
                    action: row.try_get("action").ok()?,
                })
            })
            .collect())
    }

    pub async fn upsert_rule(&self, rule: &RoutingRule) -> Result<(), String> {
        sqlx::query(
            "INSERT INTO routing_rules (id, merchant_id, name, priority, condition, action, enabled, updated_at)
             VALUES ($1,$2,$3,$4,$5,$6,$7,NOW())
             ON CONFLICT (id) DO UPDATE SET
                merchant_id = EXCLUDED.merchant_id,
                name = EXCLUDED.name,
                priority = EXCLUDED.priority,
                condition = EXCLUDED.condition,
                action = EXCLUDED.action,
                enabled = EXCLUDED.enabled,
                updated_at = NOW()",
        )
        .bind(&rule.id)
        .bind(&rule.merchant_id)
        .bind(&rule.name)
        .bind(rule.priority)
        .bind(&rule.condition)
        .bind(&rule.action)
        .bind(rule.enabled)
        .execute(&self.pool)
        .await
        .map_err(|e| e.to_string())?;
        Ok(())
    }

    pub async fn upsert_processor(&self, p: &Processor) -> Result<(), String> {
        let currencies: Vec<String> = p.supported_currencies.clone();
        let collect: Vec<String> = p.collect_rails.iter().map(|r| r.as_str().to_string()).collect();
        let payout: Vec<String> = p.payout_rails.iter().map(|r| r.as_str().to_string()).collect();
        sqlx::query(
            "INSERT INTO processors
                (id, name, enabled, priority, cost_percentage, supported_currencies, collect_rails, payout_rails, updated_at)
             VALUES ($1,$2,$3,$4,$5,$6,$7,$8,NOW())
             ON CONFLICT (id) DO UPDATE SET
                name = EXCLUDED.name,
                enabled = EXCLUDED.enabled,
                priority = EXCLUDED.priority,
                cost_percentage = EXCLUDED.cost_percentage,
                supported_currencies = EXCLUDED.supported_currencies,
                collect_rails = EXCLUDED.collect_rails,
                payout_rails = EXCLUDED.payout_rails,
                updated_at = NOW()",
        )
        .bind(p.id.as_str())
        .bind(&p.name)
        .bind(p.enabled)
        .bind(p.priority)
        .bind(p.cost_percentage)
        .bind(&currencies)
        .bind(&collect)
        .bind(&payout)
        .execute(&self.pool)
        .await
        .map_err(|e| e.to_string())?;
        Ok(())
    }

    pub async fn get_decision(&self, payment_id: &str) -> Result<Option<RouteDecision>, String> {
        let row = timeout(
            self.timeout,
            sqlx::query("SELECT decision_metadata FROM routing_decisions WHERE payment_id = $1")
                .bind(payment_id)
                .fetch_optional(&self.pool),
        )
        .await
        .map_err(|_| "postgres timeout".to_string())?
        .map_err(|e| e.to_string())?;
        match row {
            Some(r) => {
                let value: serde_json::Value = r.try_get("decision_metadata").map_err(|e| e.to_string())?;
                serde_json::from_value(value).map(Some).map_err(|e| e.to_string())
            }
            None => Ok(None),
        }
    }

    pub async fn insert_decision(&self, d: &RouteDecision, merchant_id: Option<&str>) -> Result<(), String> {
        let meta = serde_json::to_value(d).map_err(|e| e.to_string())?;
        sqlx::query(
            "INSERT INTO routing_decisions
                (id, payment_id, merchant_id, selected_processor, rail, direction, score, routing_strategy, decision_metadata)
             VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9)
             ON CONFLICT (payment_id) DO NOTHING",
        )
        .bind(&d.routing_id)
        .bind(&d.payment_id)
        .bind(merchant_id)
        .bind(d.selected_processor.as_str())
        .bind(d.rail.as_str())
        .bind(match d.direction {
            Direction::Inbound => "INBOUND",
            Direction::Outbound => "OUTBOUND",
        })
        .bind(d.score)
        .bind(&d.routing_strategy)
        .bind(meta)
        .execute(&self.pool)
        .await
        .map_err(|e| e.to_string())?;
        Ok(())
    }

    pub async fn insert_outcome(
        &self,
        routing_id: &str,
        payment_id: &str,
        processor: &str,
        success: bool,
        latency_ms: Option<f64>,
        failure_class: Option<&str>,
        counts_toward_circuit: bool,
        use_fallback: bool,
        triage: &serde_json::Value,
    ) -> Result<(), String> {
        sqlx::query(
            "INSERT INTO routing_outcomes
                (routing_id, payment_id, processor, success, latency_ms, failure_class,
                 counts_toward_circuit, use_fallback, triage)
             VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9)",
        )
        .bind(routing_id)
        .bind(payment_id)
        .bind(processor)
        .bind(success)
        .bind(latency_ms)
        .bind(failure_class)
        .bind(counts_toward_circuit)
        .bind(use_fallback)
        .bind(triage)
        .execute(&self.pool)
        .await
        .map_err(|e| e.to_string())?;
        Ok(())
    }
}

#[allow(dead_code)]
fn parse_rail(raw: &str) -> Option<Rail> {
    crate::routing::normalize::parse_rail(Some(raw))
}

#[allow(dead_code)]
fn parse_psp(raw: &str) -> Option<Psp> {
    Psp::parse(raw)
}
