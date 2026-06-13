# Orbit — design direction (2026)

**Problem with v1:** it's a "grayscale graveyard of safe spacing and polite typography" — reads like 2010 / a budget Linear knock-off. No real avatars, no warmth, no crafted feel, no per-row reasoning.

**Target feel:** a premium, opinionated *second-inbox* — the bar set by Superhuman, Beeper, Texts, and WhatsApp/Telegram.

## Principles (from research)
- **Humanize with real imagery.** Profile photos (DPs) + group pictures as avatars, with presence/initials fallback. This is the single biggest "feels real" lever. (WhatsApp/Telegram; 2026 trend: "profile pictures humanize the chat experience.")
- **Crafted, not templated.** Move off pure grayscale → a warm, confident accent + soft surfaces. "Flashes of visual audacity… interfaces that dare to feel crafted." (Envato 2026.)
- **Breathing room + clear hierarchy.** Comfortable spacing, avatars/text not crammed; legible type that "breathes." (Beeper premium feel.)
- **Opinionated structure.** Lanes/priority like Superhuman's Split Inbox (VIP / P0 / needs-reply). Orbit's tabs + scoring are this — lean in.
- **Soft per-sender color in busy groups.** (Beeper.) Helps scan group rows.
- **Reasoning on every row** (local, no tokens): a short "why it ranks" line/chips on *every* item, not just some.
- **Delight in the empty state** (Superhuman rewards inbox-zero).

## Concrete fixes for Orbit
- Real **DP / group-photo avatars** (fetch from WhatsApp, cache locally), presence dot, initials fallback.
- Clean **phone display** for unknowns (no masked dots / raw long numbers); resolve names harder.
- Warmer surface + a real accent identity (not near-black gray). Tasteful depth (soft shadow/elevation on hover), not flat-gray.
- Tighter, more confident **type scale**; numbers tabular.
- **Per-row reason** ("tagged you", "you reply fast to them", "intro", "16d quiet — was a warm lead") — every row.
- Polished tab/filter bar, search, empty states, dark + light.

## References
- Beeper relaunch (premium all-in-one): https://techcrunch.com/2025/07/16/beepers-all-in-one-messaging-app-relaunches-with-an-on-device-model-and-premium-upgrades/
- Superhuman inbox (Mobbin): https://mobbin.com/explore/screens/85b28b16-cb18-4a3c-824e-8f4fab7ba110
- 2026 UI trends (Envato): https://elements.envato.com/learn/ux-ui-design-trends
- Chat UI patterns 2026: https://bricxlabs.com/blogs/message-screen-ui-deisgn
- WhatsApp / Telegram UI kits (Figma): https://www.figma.com/community/file/874576344344319149/whatsapp-ui-screens

> Build only after Abhay confirms direction + answers persona/feature questions. Real screenshots of live apps not feasible from here; use these references + render reference mockups via the visualize tool during the build.
