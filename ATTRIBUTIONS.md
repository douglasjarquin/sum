# Attributions

I am building sum for myself, to the highest standard I can achieve. I am not trying to grow a user base for it, and I would rather you use the projects below than adopt sum. They shaped this project in concrete ways and deserve the credit. Please explore them, use the ones that fit your work, and support their maintainers.

This page distinguishes conceptual inspiration, direct dependency, adapted code, historical lineage, and integrations still being evaluated. Replacing a dependency later does not erase the credit recorded here. It supplements, and never replaces, the license and source notices carried alongside any copied material.

## Where sum comes from

**[Firstmate](https://github.com/kunchenguid/firstmate)** is the origin of **the agent-distro concept that inspired sum**: an ordinary coding harness, portable instructions/skills/helpers, and one liaison coordinating workers. That shape is sum's lineage, not a claim that Firstmate invented agent orchestration generally. If the idea of one coordinator delegating to disposable workers appeals to you, try Firstmate itself first.

**[Consigliere](https://github.com/douglasjarquin/consigliere)** was the author's own earlier, Firstmate-derived experiment: delegation conventions and the light mafia identity sum still carries came from there. It is respectful lineage, not a story about something that didn't work.

## Terminal and session runtime

**[Herdr](https://github.com/herdrdev/herdr)** ([site](https://herdr.dev/)) is the terminal runtime sum is native to: pane, session, and worktree operations are Herdr's own mechanics, and sum deliberately lets an existing runtime own them instead of reimplementing process/session management.

**[Herdr Mesh](https://github.com/runchr-works/herdr-mesh)** supplied the early, concrete MCP bridge and the small shared pane/agent tool surface that sum's own Mesh builtin now implements natively. That credit stands even after internalizing ownership of the implementation.

**[Unpeel](https://unpeel.com/)** is a conceptual inspiration for **MCP pane/session management** and cross-harness coordination — not a claim that any implementation here was copied from it. Worth exploring if you want a more general take on coordinating multiple harnesses through MCP.

**[Solo](https://soloterm.com/)** ([meta-harness explanation](https://soloterm.com/blog/the-agentic-metaharness)) is where the **meta-harness framing** sum uses comes from, along with wakeups, presets, handoffs, selective context, environment awareness, and visibility as ideas worth having regardless of which terminal you run them in. If you want a polished desktop application built around this idea rather than a small CLI helper, Solo is that product.

**[Delta](https://delta.dev/)** shaped how sum thinks about durable, thread-centered work: keeping the conversation and code context attached to a unit of work, and carrying that through handoff and review.

**[Oh My Pi](https://github.com/can1357/oh-my-pi#09--unapologetically-native-even-on-windows)** is the inspiration behind sum's preference for **builtins and reducing avoidable process boundaries** — running logic in-process instead of shelling out where it is practical to do so. This is inspiration, not a claim of an identical architecture or of matching benchmark results.

## Provider quota and evidence

**[quota-axi](https://github.com/kunchenguid/quota-axi)** shaped how sum thinks about provider evidence: freshness and uncertainty made explicit, compact output, and separating collection from policy. It is also the original dependency and inspiration for the author's independent Go sister project, **[Remainder](https://github.com/douglasjarquin/remainder)**. Sum and Pinchos each consume Remainder independently; neither treats it as a sum builtin. Credit to quota-axi stands on its own, without an unmeasured performance comparison.

## Verification and evidence

**[Atlas verification example](https://github.com/poteto/verification-skill-example/blob/main/.cursor/skills/verify-atlas/SKILL.md)** demonstrates feature-driven, real-user-path verification backed by observable evidence — the pattern sum's own verification skills follow. It is one worked example, not a supplied universal recorder or driver.

**[Cursor pstack skills](https://github.com/cursor/plugins/tree/main/pstack/skills)**, in particular [create-verification-skill](https://github.com/cursor/plugins/blob/main/pstack/skills/create-verification-skill/SKILL.md) and [maintain-verification-skill](https://github.com/cursor/plugins/blob/main/pstack/skills/maintain-verification-skill/SKILL.md), shaped how sum creates and maintains project verification contracts and feature maps, and how it selects useful upstream skills at all. Individual imported-source attribution is added here as specific skill imports actually land.

**[before-and-after](https://github.com/vercel-labs/before-and-after)** shaped how sum presents and publishes before/after media in a pull request — the presentation and publication step, distinct from the capture itself. Any adapted material keeps its original notices.

## Code exploration

**[codegraph](https://github.com/colbymchenry/codegraph)** provides the structural code context and worktree-local graph exploration every checkout sum creates gets its own index of. Credit reflects the current pinned-binary, per-checkout-index relationship, not earlier planning language.

## Verification companions

**[MADE](https://github.com/douglasjarquin/made)** is the author's existing, independent candidate-bound verification and review companion. It is a separate project with its own relationship to No Mistakes; sum does not conflate the two.

## Bot deployment

**[Grok Ship](https://github.com/kunchenguid/grok-ship)** and the [native Firstmate/Bot template](https://x.ai/bot/__4FfrkUdvpdMk6-LKg5r) it distributes shaped sum's single user-facing Bot / project-Bot deployment model. Where a successor replaces one of these, the historical reference stays.

**[Grok Ship Steward](https://github.com/douglasjarquin/grok-ship-steward)** inspired square, sum's scoped backup/recovery stewardship companion. Any upstream credit Grok Ship Steward itself carries is retained where its material is adapted.

## Tooling and supporting ecosystem

**[mise](https://mise.jdx.dev/)** underlies sum's tool/version/task setup and its portable verification foundations.

**[herdr-mirror](https://github.com/nikok6/herdr-mirror)** was evaluated as a related remote-visibility reference during remote planning. It is labeled evaluated, not installed: sum does not depend on it.

Beyond the named projects above, sum also rests on the broader Agent Skills convention, Git, and the coding harnesses it launches — none of them sum's own work, all of them worth understanding on their own terms.

## The four you'll see most

If you read nothing else on this page: **Firstmate** shaped the agent-distro concept, **Oh My Pi** shaped the preference for builtins, **Solo** shaped the meta-harness framing, and **Unpeel** shaped MCP pane/session management. Go look at all four.
