DO $$ BEGIN
 IF EXISTS(SELECT 1 FROM idenqa.assurance_profiles) OR EXISTS(SELECT 1 FROM idenqa.policy_assurance_assignments) OR EXISTS(SELECT 1 FROM idenqa.verification_assurance WHERE profile_name IS NOT NULL) THEN
  RAISE EXCEPTION 'cannot remove assurance schema while immutable profiles or assignments exist';
 END IF;
END $$;
DROP TABLE idenqa.verification_assurance;
DROP TABLE idenqa.policy_assurance_assignments;
DROP TABLE idenqa.assurance_profiles;
