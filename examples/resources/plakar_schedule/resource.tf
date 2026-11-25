# The source and the store the schedules below run on, looked up by name so
# the example stands alone.
data "plakar_connector" "db" {
  name = "Production DB"
  type = "source"
}

data "plakar_store" "offsite" {
  name = "Offsite S3"
}

resource "plakar_schedule" "nightly" {
  name      = "Nightly database backup"
  type      = "backup"
  origin_id = data.plakar_connector.db.id
  target_id = data.plakar_store.offsite.id
  labels    = ["nightly"]

  rule {
    periodicity = 86400
  }
}

resource "plakar_schedule" "retention" {
  name      = "Retention policy"
  type      = "prune"
  origin_id = data.plakar_store.offsite.id
  group_by  = "dataset"
  retention = {
    day     = 7
    per_day = 1
  }

  rule {
    periodicity = 86400
  }
}
