CREATE TABLE endpoints (
                           id          UUID PRIMARY KEY,
                           user_id     UUID NOT NULL,
                           url         TEXT NOT NULL,
                           secret      TEXT NOT NULL,
                           active      BOOLEAN NOT NULL DEFAULT true,
                           created_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX endpoints_user_idx ON endpoints (user_id);

CREATE TABLE endpoint_subscriptions (
                                        endpoint_id UUID NOT NULL REFERENCES endpoints(id) ON DELETE CASCADE,
                                        event_type  TEXT NOT NULL,
                                        PRIMARY KEY (endpoint_id, event_type)
);

CREATE TABLE deliveries (
                            id               UUID PRIMARY KEY,
                            event_id         UUID NOT NULL,
                            endpoint_id      UUID NOT NULL REFERENCES endpoints(id) ON DELETE CASCADE,
                            user_id          UUID NOT NULL,
                            event_type       TEXT NOT NULL,
                            payload          JSONB NOT NULL,
                            status           TEXT NOT NULL
                                CHECK (status IN
                                       ('pending','queued','in_flight','succeeded','dead')),
                            attempts         INT NOT NULL DEFAULT 0,
                            next_attempt_at  TIMESTAMPTZ,
                            created_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
                            updated_at       TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX deliveries_user_idx     ON deliveries (user_id, id DESC);
CREATE INDEX deliveries_endpoint_idx ON deliveries (endpoint_id, id DESC);
CREATE INDEX deliveries_due_idx      ON deliveries (next_attempt_at)
    WHERE status = 'pending' AND next_attempt_at IS NOT NULL;

CREATE TABLE delivery_attempts (
                                   id              BIGSERIAL PRIMARY KEY,
                                   delivery_id     UUID NOT NULL REFERENCES deliveries(id) ON DELETE CASCADE,
                                   attempt_number  INT NOT NULL,
                                   started_at      TIMESTAMPTZ NOT NULL,
                                   finished_at     TIMESTAMPTZ NOT NULL,
                                   status_code     INT,
                                   error           TEXT,
                                   duration_ms     INT NOT NULL
);
CREATE INDEX delivery_attempts_delivery_idx
    ON delivery_attempts (delivery_id, attempt_number);

CREATE TABLE seen_events (
                             event_id    UUID PRIMARY KEY,
                             received_at TIMESTAMPTZ NOT NULL DEFAULT now()
);