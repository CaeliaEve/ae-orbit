ALTER TABLE members ADD COLUMN game_id text NOT NULL DEFAULT '';
ALTER TABLE members ADD COLUMN password_salt bytea;
ALTER TABLE members ADD COLUMN password_hash bytea;
ALTER TABLE members ADD COLUMN auth_version bigint NOT NULL DEFAULT 0;
CREATE UNIQUE INDEX members_game_id_unique ON members(lower(game_id)) WHERE game_id<>'';
ALTER TABLE members ADD CONSTRAINT members_game_id_format CHECK(game_id='' OR game_id ~ '^[A-Za-z0-9_]{3,16}$');

CREATE TABLE player_invitations (
    id text PRIMARY KEY,
    code_hash bytea NOT NULL UNIQUE,
    network_id text NOT NULL REFERENCES networks(id) ON DELETE CASCADE,
    created_by text NOT NULL REFERENCES members(id),
    can_craft boolean NOT NULL,
    max_uses integer NOT NULL CHECK(max_uses BETWEEN 1 AND 100),
    uses integer NOT NULL DEFAULT 0 CHECK(uses>=0 AND uses<=max_uses),
    expires_at timestamptz NOT NULL,
    revoked boolean NOT NULL DEFAULT false,
    created_at timestamptz NOT NULL DEFAULT now()
);

-- Empty snapshots explicitly retain legacy/shared-account attribution.
ALTER TABLE production_jobs ADD COLUMN owner_name text NOT NULL DEFAULT '';
ALTER TABLE orders ADD COLUMN owner_name text NOT NULL DEFAULT '';
ALTER TABLE production_job_actions ADD COLUMN actor_name text NOT NULL DEFAULT '';
UPDATE production_jobs j SET owner_name=m.name FROM members m WHERE m.id=j.member_id;
UPDATE orders o SET owner_name=m.name FROM members m WHERE m.id=o.member_id;
UPDATE production_job_actions a SET actor_name=m.name FROM members m WHERE m.id=a.member_id;

CREATE FUNCTION stamp_player_identity() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE person text;
BEGIN
    SELECT coalesce(nullif(game_id,''),name) INTO person FROM members WHERE id=NEW.member_id;
    IF TG_TABLE_NAME='production_job_actions' THEN NEW.actor_name:=coalesce(person,'');
    ELSIF TG_TABLE_NAME='orders' THEN
        SELECT j.owner_name INTO NEW.owner_name FROM production_job_entries e JOIN production_jobs j ON j.id=e.job_id
          WHERE e.id=NEW.production_job_entry_id;
        NEW.owner_name:=coalesce(NEW.owner_name,person,'');
    ELSE NEW.owner_name:=coalesce(person,''); END IF;
    RETURN NEW;
END $$;
CREATE TRIGGER production_jobs_identity BEFORE INSERT ON production_jobs FOR EACH ROW EXECUTE FUNCTION stamp_player_identity();
CREATE TRIGGER orders_identity BEFORE INSERT ON orders FOR EACH ROW EXECUTE FUNCTION stamp_player_identity();
CREATE TRIGGER job_actions_identity BEFORE INSERT ON production_job_actions FOR EACH ROW EXECUTE FUNCTION stamp_player_identity();
