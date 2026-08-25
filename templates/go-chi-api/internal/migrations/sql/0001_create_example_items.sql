create table example_items (
  id uuid primary key,
  name text not null,
  created_at timestamptz not null default now()
);
