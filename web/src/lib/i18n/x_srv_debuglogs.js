// SPDX-License-Identifier: AGPL-3.0-only
// German translations – "router sends debug logs" suggestion (#236)
export default {
  "{name} sends debug logs to mtmon": "{name} schickt Debug-Logs an mtmon",
  "A logging rule with a plain topic also matches that topic's debug messages (DHCP packet dumps, WireGuard keepalives). They fill the device event log and add nothing. mtmon drops them on arrival, but the router still sends them.": "Eine Logging-Regel mit einfachem Topic erfasst auch dessen Debug-Meldungen (DHCP-Paketdumps, WireGuard-Keepalives). Sie füllen das Geräte-Ereignislog und bringen nichts. mtmon verwirft sie beim Empfang, der Router sendet sie aber weiter.",
  "Run the commands on the router: they exclude debug messages from the rules the setup wizard created for mtmon.": "Befehle auf dem Router ausführen: sie nehmen Debug-Meldungen aus den Regeln heraus, die der Einrichtungsassistent für mtmon angelegt hat.",
  "Rules you made yourself for the same action with other topics are not touched; check them with /system logging print.": "Eigene Regeln für dieselbe Action mit anderen Topics bleiben unberührt; mit /system logging print prüfen.",
}
