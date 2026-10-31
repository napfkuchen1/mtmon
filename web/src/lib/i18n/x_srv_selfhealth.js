// SPDX-License-Identifier: AGPL-3.0-only
// German translations of backend text – mtmon health suggestion
export default {
  "mtmon's disk is filling up": "Der Speicherplatz von mtmon füllt sich",
  "mtmon's disk is almost full": "Der Speicherplatz von mtmon ist fast voll",
  "When the disk is full, SQLite cannot write any more: flows, alerts and history are lost and monitoring effectively stops.": "Ist der Speicher voll, kann SQLite nichts mehr schreiben: Flows, Alarme und Verlauf gehen verloren, das Monitoring steht praktisch still.",
  "Free space where mtmon stores its data: {free} of {total} ({p} %)": "Freier Platz, auf dem mtmon seine Daten speichert: {free} von {total} ({p} %)",
  "Database size: {size}": "Größe der Datenbank: {size}",
  "Growth: about {size} per day – the disk would be full in about {n} days": "Wachstum: etwa {size} pro Tag – der Speicher wäre in etwa {n} Tagen voll",
  "Lower raw_retention_days (now {n}) in /etc/mtmon/config.json and restart mtmon; the raw flows are the largest part of the database.": "Verringere raw_retention_days (jetzt {n}) in /etc/mtmon/config.json und starte mtmon neu; die Roh-Flows machen den größten Teil der Datenbank aus.",
  "Or lower retention_days, the history of the rollups (now {n}).": "Oder verringere retention_days, den Verlauf der Rollups (jetzt {n}).",
  "Or give the container more disk (Proxmox: resize the root disk, then restart the container).": "Oder gib dem Container mehr Speicher (Proxmox: Root-Disk vergrößern, dann Container neu starten).",
  "The forecast is a straight line over the last days; it is only shown once mtmon has watched the database for 6 hours.": "Die Prognose ist eine Gerade über die letzten Tage; sie erscheint erst, wenn mtmon die Datenbank 6 Stunden beobachtet hat.",
}
