
The projection follows DCAT 3: `dcat:hasCurrentVersion`, `dcat:hasVersion`, `dcat:version`, `dcat:previousVersion`, `prov:wasRevisionOf`, `prov:wasAttributedTo`, `dct:issued`. A snapshot carries the document's own class. Ordering is by `dcat:version` as a number, because `/10` sorts before `/3` as a string.

Each version is its own immutable named graph. The canonical node lives in a small mutable graph holding the current-version pointer. Publishing a version is one transaction: insert the version graph, retarget the pointer. No reader observes a pointer disagreeing with its content.

