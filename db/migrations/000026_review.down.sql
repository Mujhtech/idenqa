DROP TRIGGER IF EXISTS review_findings_immutable ON idenqa.review_findings;
DROP TABLE IF EXISTS idenqa.appeals;
DROP TABLE IF EXISTS idenqa.review_findings;
DROP TABLE IF EXISTS idenqa.review_cases;
DROP FUNCTION IF EXISTS idenqa.protect_review_finding();
