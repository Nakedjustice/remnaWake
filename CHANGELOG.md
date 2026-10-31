# Changelog

## Unreleased

### Fixed

- Confirmed subscription downgrades now reset current Remnawave traffic usage to zero after applying the new plan limits, while preserving active traffic-extension allowances.
- Traffic-extension purchases no longer hide the latest subscription purchase during plan-change detection.
- Bot and Mini App downgrade confirmations now disclose the immediate limit change and traffic reset before payment.
- Mini App tariff changes now use visible in-page Continue and Cancel controls instead of a native confirmation dialog that could leave the Pay button unresponsive.

### Build

- Added `.dockerignore` to keep local secrets, caches, databases, and build artifacts out of Docker build contexts.
