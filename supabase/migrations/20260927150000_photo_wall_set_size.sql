-- The sign-in photo wall keeps a set of photographs in memory and scrolls
-- through it (docs/design/signin-photo-wall.html). This is how many. 400 is
-- the ceiling: at about 100 KB a tile that is 40 MB of the server's memory,
-- and more than a strip needs to look fresh all day.
alter table app_settings
  add column photo_wall_size integer not null default 150
    constraint app_settings_photo_wall_size check (photo_wall_size between 10 and 400);
