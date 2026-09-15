-- Refuse rollback while routed cases exist rather than deleting their lineage.
ALTER TABLE idenqa.review_cases ALTER COLUMN challenged_decision_id SET NOT NULL;
DROP TRIGGER protect_review_origin ON idenqa.review_cases;
DROP FUNCTION idenqa.protect_review_origin();
ALTER TABLE idenqa.review_cases DROP CONSTRAINT review_case_origin;
ALTER TABLE idenqa.review_cases DROP CONSTRAINT review_case_routing_fk;
DROP INDEX idenqa.review_case_routing_unique;
ALTER TABLE idenqa.review_cases DROP COLUMN routing_request_id;
ALTER TABLE idenqa.review_cases DROP CONSTRAINT review_case_region;
ALTER TABLE idenqa.review_cases ADD CONSTRAINT review_case_region CHECK (region ~ '^[a-z][a-z0-9-]{0,62}$');
DROP TABLE idenqa.policy_routing_receipts;
