# Re-org: governance, cost reports and the coordinator move under ee/ (owner ruling 2026-10-29)

Owner: the Fugaro owner. Verbatim rulings:
- "There's no such 'what's already published under MIT stays there' ruling. We have to re-org project to meet business needs."
- "governance should be under ee/. We said that cost reports belong there as well, no? As well as the coordinator agent, right?"
- Answers: hold the 0.13.0 tag until governance is under ee/; governance = budgets, tiers, tripwires, checkpoints, stage timeouts AND digest reports (reverses the earlier T11 ruling that `fugaro runs digest` stays MIT); governance work in flight finishes in internal/ and moves once.
- Standing earlier rulings (2026-10-27, docs/subscriptions/): A5 coordinator (command + library, future versions) moves to the business-licensed folder; A15 spend and cost reports (`fugaro report` and similar) move; the credentials service's audit log stays MIT; A18 key-gated single binary (`ee` build tag); T27 paid commands WARN, not refuse, until enforcement; A6 central orchestrators and the hosted MCP server start in the business folder.
