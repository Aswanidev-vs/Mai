# DESIGN.md — Mai GitHub App

## 1. Objective

Mai's website should make visitors feel like they're meeting a capable, friendly AI assistant who happens to be an anime character. The quality bar: every page load feels like opening a well-crafted app, not a generic SaaS landing page. The site should feel alive, slightly playful, and technically credible.

## 2. Product Context

- **What the product does:** Mai is a GitHub bot that auto-reviews PRs, resolves merge conflicts, and fixes code issues
- **Who it's for:** Developers who want automated code review without leaving GitHub
- **Adjacent brands (feel like these):** CodeRabbit (AI review), Linear (clean product UI), Vercel (developer-focused polish)
- **Distant brand (do not feel like this):** Notion (too corporate/enterprise), old GitHub (too utilitarian)
- **Cultural register:** Playful-technical — anime aesthetic meets serious developer tooling

## 3. Visual Foundations

### 3a. Color

- **Neutral scale:** `--n-950: #0a0a0f, --n-900: #12121a, --n-800: #1a1a2e, --n-700: #2a2a3e, --n-600: #4a4a5e, --n-400: #8888a0, --n-200: #c8c8d8, --n-100: #e8e8f0, --n-50: #f5f5fa`
- **Accent primary:** `--accent: #9b59b6` (Mai's purple eyes)
- **Accent secondary:** `--accent-pink: #e88ca5` (ribbon color)
- **Semantic:** `--success: #4ade80, --warning: #fbbf24, --error: #f87171`
- **Usage rules:** Purple accent on CTAs and highlights only. Pink for decorative touches. Dark backgrounds throughout.

### 3b. Typography

- **Display face:** `Space Grotesk` weights 500-700, tight tracking
- **Body face:** `Inter` weights 400-500
- **Fallback stack:** `system-ui, -apple-system, sans-serif`
- **Type scale:** 14 / 16 / 18 / 24 / 32 / 48 / 64 / 80
- **Weight discipline:** Display for headlines only. Body for everything else. No bold body text.

### 3c. Spacing & rhythm

- **Base unit:** 8px
- **Spacing scale:** 8, 16, 24, 32, 48, 64, 96, 128 px
- **What "generous" whitespace means:** Section padding >= 96px on desktop, 64px on mobile

### 3d. Component seeds

- **Button:** Two variants — primary (filled purple, rounded-lg) and ghost (border, transparent). No other variants.
- **Card / container:** Glass-morphism cards with subtle blur and border. Not flat white cards.
- **Iconography:** Lucide icons, stroke weight 1.5
- **Special:** Floating Mai character illustration that follows scroll

## 4. Accessibility

- **Text contrast:** Body text 4.5:1 min against dark backgrounds
- **Motion:** Respect prefers-reduced-motion, disable parallax and floating animations
- **Focus indicators:** Purple outline ring, 2px offset
- **Alt text policy:** Mai's image: "Mai, an anime-style AI assistant with black hair and purple eyes"

## 5. Voice & Tone

- **Register:** Conversational-technical
- **Sentence rhythm:** Short, punchy. No filler.
- **Words this brand uses:** "review", "fix", "resolve", "auto"
- **Words this brand refuses:** "seamless", "elevate", "journey", "unlock", "delight", "powerful"
- **Address:** "you" — direct, second person

## 6. Implementation Practices

- **Token format:** CSS variables
- **Component library:** Bespoke (hand-crafted for this brand)
- **Image treatment:** Mai character as PNG cutout, no stock photos
- **Grid system:** 12-col, asymmetric where possible
- **Motion rules:** ease-out 300ms for interactions, stagger reveals on scroll

## 7. Anti-Patterns

- **No gradient hero.** Mai's character IS the hero, not a purple blur behind text.
- **No rounded-16px card grid.** Glass cards with purpose, not cookie-cutter grids.
- **No emoji section headers.** Typography does the work.
- **No "seamlessly" or "elevate."** Show, don't tell.
- **No generic AI illustration.** The anime character is the brand.

## 8. Decision-Making

1. **Character-first.** If a decision conflicts with showcasing Mai, Mai wins.
2. **Developer credibility.** Every technical claim must be specific and verifiable.
3. **Restraint over decoration.** If an element doesn't serve a purpose, remove it.
4. **Dark theme default.** Developers expect it; Mai's aesthetic demands it.

## 9. Workflow

1. Start with dark canvas, not white
2. Place Mai's character as the visual anchor
3. Write headlines before designing around them
4. Build the nav, hero, and CTA first — these carry 80% of the impression
5. Add feature sections only if they add new information
6. Test at 320px and 1440px before anything in between
7. Run anti-slop pass before shipping
