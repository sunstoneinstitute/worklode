
The Morning Brief is computed from governed events since the actor's
boundary. It groups by project, showing the project's focus note, and orders
within a group: decisions and exceptions needing the actor (derived from open
state, so they persist across briefs); material outcomes and changes; runs
that stopped or reached a bound; routine successful work, collapsed to a
count. One render reads a bounded number of events and says when it was cut
short. Opening Home never marks the brief consumed; an explicit **Reviewed
through now** action (`POST /home/reviewed`) advances the actor's boundary to
the displayed cutoff, forward only and never past the event log. Worklode
schedules no wake-up, email, chat or off-hours notification for orchestrated
work; production alerts stay in the operational alerting system.

