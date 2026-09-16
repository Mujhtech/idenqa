DO $$ BEGIN
 IF EXISTS(SELECT 1 FROM idenqa.model_registry_history) THEN
  RAISE EXCEPTION 'cannot discard immutable model registry history';
 END IF;
END $$;
ALTER TABLE idenqa.model_requests DROP CONSTRAINT model_request_registry_history_fk, DROP CONSTRAINT model_request_registry_selection, DROP COLUMN registry_name, DROP COLUMN registry_version, DROP COLUMN registry_selection;
DROP TABLE idenqa.model_registry_history;
DROP TABLE idenqa.model_registry_revisions;
DROP TABLE idenqa.model_registries;
