resource "plakar_schedule" "nightly" {
  name      = "Nightly database backup"
  type      = "backup"
  origin_id = data.plakar_connector.db.id
  target_id = plakar_store.offsite.id
  labels    = ["nightly"]

  rule {
    periodicity = 86400
  }
}

resource "plakar_schedule" "retention" {
  name      = "Retention policy"
  type      = "prune"
  origin_id = plakar_store.offsite.id
  group_by  = "dataset"
  retention = {
    day     = 7
    per_day = 1
  }

  rule {
    periodicity = 86400
  }
}
