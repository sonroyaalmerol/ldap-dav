create table if not exists calendars (
  id uuid primary key,
  owner_user_id text,
  owner_group text,
  uri text not null,
  display_name text,
  description text,
  ctag text not null,
  color varchar(7) default '#3174ad',
  sync_seq bigint not null default 0,
  sync_token text not null default 'seq:0',
  created_at timestamptz not null default now(),
  updated_at timestamptz not null default now()
);

create unique index if not exists calendars_owner_uri_unique
  on calendars(owner_user_id, uri);

create table if not exists calendar_objects (
  id uuid primary key,
  calendar_id uuid not null references calendars(id) on delete cascade,
  uid text not null,
  etag text not null,
  data text not null,
  component text not null,
  start_at timestamptz,
  end_at timestamptz,
  updated_at timestamptz not null default now()
);

create unique index if not exists objects_calendar_uid_unique
  on calendar_objects(calendar_id, uid);

create table if not exists calendar_changes (
  calendar_id uuid not null references calendars(id) on delete cascade,
  seq bigint not null,
  uid text not null,
  deleted boolean not null default false,
  changed_at timestamptz not null default now(),
  primary key (calendar_id, seq)
);

create index if not exists calendar_changes_calendar_seq_idx
  on calendar_changes(calendar_id, seq);

create index if not exists idx_objects_cal_comp_time
  on calendar_objects(calendar_id, component, start_at, end_at);

create index if not exists idx_changes_cal_seq
  on calendar_changes(calendar_id, seq);

create index if not exists idx_objects_cal_updated
  on calendar_objects(calendar_id, updated_at);
