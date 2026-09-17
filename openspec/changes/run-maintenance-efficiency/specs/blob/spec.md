## ADDED Requirements

### Requirement: Metadata-only run retention

Run retention MUST determine object age from recursive provider listings.
Remote adapters MUST use the listing's provider version without downloading
object content. The filesystem adapter MUST use a persisted write generation
without reading object content.

#### Scenario: Expired artifacts

- GIVEN a listing returns an object older than the retention cutoff with a version
- WHEN retention evaluates the object
- THEN it MUST issue a conditional delete for the listed version without a content read.

#### Scenario: Filesystem object version

- GIVEN retention lists an object in the filesystem adapter
- WHEN the adapter creates the listed version
- THEN it MUST use a write generation that changes for every successful write

#### Scenario: Filesystem object is rewritten with identical content

- GIVEN retention lists an object in the filesystem adapter
- WHEN the object is rewritten with identical content before deletion
- THEN conditional deletion MUST reject the listed version

#### Scenario: Filesystem object churn

- GIVEN filesystem artifacts and audit records use many unique keys
- WHEN the adapter coordinates unconditional and conditional writes
- THEN its permanent lock storage MUST remain bounded by a fixed shard count
- AND legacy per-key locks MUST be limited to the locks namespace

#### Scenario: Incomplete listing metadata

- GIVEN a listing omits an object's modification time or version
- WHEN retention evaluates the object
- THEN it MUST retain the object.

#### Scenario: Concurrent replacement

- GIVEN an expired object is replaced after listing
- WHEN the conditional delete rejects the listed version
- THEN retention MUST preserve the replacement and continue processing other objects.

#### Scenario: S3-compatible endpoint ignores delete conditions

- GIVEN an S3-compatible endpoint accepts a stale conditional delete
- WHEN retention first attempts conditional deletion
- THEN the adapter MUST reject the backend before deleting a user object
- AND it MUST cache the probe result for the lifetime of the adapter
- AND it MUST keep the probe inside the managed runs namespace

#### Scenario: S3 conditional-delete probe cannot complete

- GIVEN an S3-compatible endpoint returns a terminal probe error
- WHEN retention attempts conditional deletion
- THEN the adapter MUST cache and report a probe failure without retrying per object
- AND it MUST NOT report the backend as confirmed unsupported

#### Scenario: Provider maintenance contract

- GIVEN a blob adapter participates in the shared contract suite
- WHEN its maintenance operations are tested
- THEN listing MUST return a non-empty version and modification time
- AND a stale conditional delete MUST preserve the replacement
- AND unsupported conditional deletion MUST require an explicit contract expectation

#### Scenario: Adapter lacks metadata maintenance

- GIVEN a blob adapter cannot list metadata or delete a listed version conditionally
- WHEN retention starts
- THEN it MUST report the unsupported capability without falling back to content reads.
