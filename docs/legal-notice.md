# Legal Notice

This project is software for indexing and serving a prepared Markdown corpus derived from public documents on the Korea Exchange legal portal.

Source:

- https://rule.krx.co.kr/out/index.do

The repository MIT license applies to project code. It does not change the legal status, copyright, terms of use, or redistribution conditions of KRX rule text, attachments, or other source documents.

Generated data should preserve source URLs and collection timestamps. Verify official rule text directly with the Korea Exchange legal portal before relying on it. This project does not provide legal advice.

Corpus collection and attachment conversion are handled by the separate `krx-rule-markdown` project. Review converter and data redistribution obligations there before publishing generated corpus artifacts.

## Public source policy

The MCP server does not publish local raw-file paths or serve raw attachment bytes. Until an operator has independently confirmed the applicable redistribution rights, public responses provide a reproducible official-source descriptor instead: `source_url`, source title/id, MIME type, filename, byte size, collection timestamp, and content/quality metadata where available. Text resources contain only the bounded collected/converted Markdown.

If an operator later enables binary distribution outside this project, it must be a separately authenticated and size-bounded route with an allowlisted MIME/signature policy and documented rights basis. Otherwise clients should follow `source_url` to the official KRX portal. A collected URL can change or expire, so compliance-sensitive use must still locate and verify the current Korean source on the portal.
