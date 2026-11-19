-- History is append-only. Quantity evidence still belongs to inventory;
-- repeated readings do not create a second log proportional to poll traffic.
CREATE TABLE inventory_history_epochs (
    id bigserial PRIMARY KEY,
    network_id text NOT NULL REFERENCES networks(id) ON DELETE CASCADE,
    scope text NOT NULL,
    profile_at timestamptz NOT NULL DEFAULT '-infinity',
    started_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    initialized boolean NOT NULL DEFAULT false,
    UNIQUE(network_id,scope,profile_at)
);
CREATE TABLE inventory_history_items (
    epoch_id bigint NOT NULL REFERENCES inventory_history_epochs(id) ON DELETE CASCADE,
    item_id bigint NOT NULL,
    name text NOT NULL, damage integer NOT NULL, label text NOT NULL,
    search_text text NOT NULL DEFAULT '',
    quantity bigint NOT NULL CHECK(quantity>=0),
    observed_at timestamptz NOT NULL,
    PRIMARY KEY(epoch_id,item_id)
);
CREATE INDEX inventory_history_search ON inventory_history_items USING gin(search_text gin_trgm_ops);
CREATE TABLE inventory_history_records (
    id bigserial PRIMARY KEY,
    epoch_id bigint NOT NULL REFERENCES inventory_history_epochs(id) ON DELETE CASCADE,
    item_id bigint NOT NULL,
    quantity bigint NOT NULL CHECK(quantity>=0),
    previous_quantity bigint,
    kind text NOT NULL CHECK(kind IN ('baseline','first','change')),
    recorded_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    observed_at timestamptz NOT NULL,
    session_id text NOT NULL,
    CHECK((kind='change')=(previous_quantity IS NOT NULL))
);
CREATE INDEX inventory_history_records_time ON inventory_history_records(epoch_id,recorded_at DESC,id DESC);
CREATE INDEX inventory_history_records_item ON inventory_history_records(epoch_id,item_id,recorded_at DESC,id DESC);
CREATE INDEX inventory_history_records_pages ON inventory_history_records(epoch_id,id DESC);
CREATE INDEX inventory_history_records_item_pages ON inventory_history_records(epoch_id,item_id,id DESC);
CREATE TABLE inventory_history_totals (
    epoch_id bigint NOT NULL REFERENCES inventory_history_epochs(id) ON DELETE CASCADE,
    resolution integer NOT NULL CHECK(resolution IN (60,3600,86400)),
    bucket timestamptz NOT NULL,
    sampled_at timestamptz NOT NULL,
    total_quantity numeric(40,0) NOT NULL,
    minimum numeric(40,0) NOT NULL, maximum numeric(40,0) NOT NULL,
    entries bigint NOT NULL, checked bigint NOT NULL, candidates bigint NOT NULL,
    online boolean NOT NULL, all_online boolean NOT NULL,
    oldest_observation timestamptz, last_observation timestamptz,
    samples bigint NOT NULL DEFAULT 1,
    PRIMARY KEY(epoch_id,resolution,bucket)
);

CREATE FUNCTION inventory_history_epoch(n text) RETURNS bigint LANGUAGE plpgsql AS $$
DECLARE e bigint; p timestamptz; s text;
BEGIN
    SELECT enabled_at INTO p FROM network_inventory_profiles WHERE network_id=n;
    s:=CASE WHEN p IS NULL THEN 'observed_plain' ELSE 'nesql_plain' END;
    p:=coalesce(p,'-infinity'::timestamptz);
    SELECT id INTO e FROM inventory_history_epochs WHERE network_id=n AND scope=s AND profile_at=p;
    IF e IS NULL THEN
        INSERT INTO inventory_history_epochs(network_id,scope,profile_at) VALUES(n,s,p)
        ON CONFLICT(network_id,scope,profile_at) DO UPDATE SET network_id=excluded.network_id
        RETURNING id INTO e;
    END IF;
    RETURN e;
END $$;

CREATE FUNCTION record_inventory_history() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE e bigint; previous bigint; item_name text; item_label text; item_search text; item_damage integer;
BEGIN
    -- Updates that only confirm the same quantity have no historical change.
    IF TG_OP='UPDATE' AND OLD.quantity=NEW.quantity AND EXISTS(
        SELECT 1 FROM inventory_history_epochs e JOIN inventory_history_items h ON h.epoch_id=e.id
        LEFT JOIN network_inventory_profiles p ON p.network_id=e.network_id
        WHERE e.network_id=NEW.network_id AND h.item_id=NEW.item_id
          AND e.scope=CASE WHEN p.network_id IS NULL THEN 'observed_plain' ELSE 'nesql_plain' END
          AND e.profile_at=coalesce(p.enabled_at,'-infinity'::timestamptz)
    ) THEN RETURN NEW; END IF;
    SELECT d.name,d.damage,coalesce(c.label,d.label),coalesce(c.search_text,d.search_label)
    INTO item_name,item_damage,item_label,item_search
    FROM item_definitions d LEFT JOIN plain_catalog_candidates c ON c.network_id=d.network_id AND c.item_id=d.id
    WHERE d.network_id=NEW.network_id AND d.id=NEW.item_id AND d.kind='item' AND d.exact AND d.nbt=''
      AND (NOT EXISTS(SELECT 1 FROM network_inventory_profiles p WHERE p.network_id=NEW.network_id)
        OR EXISTS(SELECT 1 FROM network_inventory_profiles p WHERE p.network_id=NEW.network_id
          AND c.item_id IS NOT NULL AND NEW.observed_at>=p.enabled_at));
    IF item_name IS NULL THEN RETURN NEW; END IF;
    e:=inventory_history_epoch(NEW.network_id);
    SELECT quantity INTO previous FROM inventory_history_items WHERE epoch_id=e AND item_id=NEW.item_id FOR UPDATE;
    IF previous IS NOT NULL AND previous=NEW.quantity THEN RETURN NEW; END IF;
    INSERT INTO inventory_history_items(epoch_id,item_id,name,damage,label,search_text,quantity,observed_at)
    VALUES(e,NEW.item_id,item_name,item_damage,item_label,item_search,NEW.quantity,NEW.observed_at)
    ON CONFLICT(epoch_id,item_id) DO UPDATE SET quantity=excluded.quantity,observed_at=excluded.observed_at;
    INSERT INTO inventory_history_records(epoch_id,item_id,quantity,previous_quantity,kind,observed_at,session_id)
    VALUES(e,NEW.item_id,NEW.quantity,previous,CASE WHEN previous IS NULL THEN 'first' ELSE 'change' END,NEW.observed_at,NEW.session_id);
    RETURN NEW;
END $$;
CREATE TRIGGER inventory_history_capture AFTER INSERT OR UPDATE OF quantity,observed_at ON inventory
FOR EACH ROW EXECUTE FUNCTION record_inventory_history();
