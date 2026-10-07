# Authors

whatsapp-mcp is distributed under the [MIT License](./LICENSE). Each person below keeps the copyright of their own contributions.

## Original author

- **Luke Harries** ([@lharries](https://github.com/lharries)): created the project ([lharries/whatsapp-mcp](https://github.com/lharries/whatsapp-mcp))

## Fork maintainer

- **Rodrigo Ernesto Álvarez Aguilera** ([@incognia](https://github.com/incognia)): maintains this fork ([incognia/whatsapp-mcp](https://github.com/incognia/whatsapp-mcp))

## Code contributors

Commits included in this repository, with their original authorship kept in the Git history:

- **Matija Stepanic**: captions of images, videos and documents (upstream PR #350)
- **cherian**: media download 403 fix (upstream PR #361)
- **HalemoGPA**: `media_path` path traversal guard (upstream PR #275)
- **jmmgreg**: REST API bound to loopback by default (upstream PR #224)
- **Alonso Astroza Tagle**: Windows compatibility instructions
- **Ikko Eltociear Ashimine**, **Alain (Supervaize)**, **Abdullah Alhaider**: README fixes

## Approaches adapted

Changes reimplemented in this fork from the ideas or code of these projects and pull requests, all under the MIT License:

- [daymade/whatsapp-mcp](https://github.com/daymade/whatsapp-mcp): storing sent messages
- [LukasHaas/whatsapp-mcp](https://github.com/LukasHaas/whatsapp-mcp): address-book contact search (upstream PR #343) and on-demand history backfill
- [AdamRussak/whatsapp-mcp](https://github.com/AdamRussak/whatsapp-mcp): message listener model and match modes (ADR 0001)
- **HalemoGPA**: last message per chat (upstream PR #283)
- **Smartinny**: unique media filenames (upstream PR #270)
- Upstream PRs #229, #265, #364, #326, #183 and #191

[CHANGELOG.md](./CHANGELOG.md) credits the source of each change. If your work is used here and you are missing from this list, please open an issue.
