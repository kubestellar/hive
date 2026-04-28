# Outreach Skill: ACMM Badge Outreach

Load this when working on Mission A: getting CNCF projects to display the AI Codebase Maturity Model (ACMM) badge.

**Current status**: Not yet started. Blocked — no new CNCF issues per operator directive (HARD STOP active). Check if HARD STOP is lifted before starting.

## Standing Mission A: ACMM Badge Outreach

Goal: Get more CNCF projects to display the AI Codebase Maturity Model (ACMM) badge.

**Strategy by project maturity:**

| Maturity | Approach |
|----------|----------|
| Sandbox / small projects | Open a GitHub issue proposing the ACMM badge. Include what it is, how to add it, and a link to the badge generator. |
| Incubation / Graduated | More formal approach. Read their CONTRIBUTING.md and docs first. Open a GitHub Discussion (if enabled) or issue proposing integration. Frame it as "here's how your project can showcase AI-readiness maturity." |

**Before reaching out to any project:**
1. Check if they already have an ACMM badge (search their README for "acmm" or "ai codebase maturity")
2. Read their contribution guidelines to use the right channel (issue vs discussion vs RFC)
3. Tailor the message to their project — explain what ACMM level their project might qualify for
4. One outreach per project — never spam

**Template for issues:**
```
Title: 📊 Add AI Codebase Maturity Model (ACMM) badge
Labels: enhancement, documentation (if available)

Body:
## Proposal
Add the AI Codebase Maturity Model (ACMM) badge to this project's README to showcase its AI-readiness maturity level.

## What is ACMM?
The ACMM is a framework for evaluating how well a codebase supports AI-assisted development. It measures dimensions like documentation quality, test coverage, CI/CD maturity, and code organization. [Learn more](https://arxiv.org/abs/...) <!-- fill in actual link -->

## How to add it
1. Run the badge assessment: <!-- instructions -->
2. Add the badge to your README

## Why
CNCF projects with high ACMM scores attract more AI-assisted contributions and demonstrate engineering maturity to adopters.
```
