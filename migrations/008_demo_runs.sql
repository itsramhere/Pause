CREATE TABLE demo_runs (
 id uuid PRIMARY KEY,
 household_id uuid NOT NULL UNIQUE REFERENCES households,
 scenario text NOT NULL CHECK(scenario IN ('stopped','positive')),
 reset_hash text NOT NULL,
 created_at timestamptz NOT NULL,
 expires_at timestamptz NOT NULL,
 resetting boolean NOT NULL DEFAULT false,
 closed_at timestamptz
);
CREATE TABLE demo_launch_buckets (
 bucket_hash text PRIMARY KEY,
 attempts integer NOT NULL,
 window_start timestamptz NOT NULL
);
