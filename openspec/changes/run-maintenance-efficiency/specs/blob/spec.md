## ADDED Requirements

### Requirement: Metadata-only run retention

Run retention MUST determine object age and version from recursive provider listings.
It MUST NOT open object content to decide whether an object has expired.

#### Scenario: Expired artifacts

- GIVEN a listing returns an object older than the retention cutoff with a version
- WHEN retention evaluates the object
- THEN it MUST issue a conditional delete for the listed version without a content read.

#### Scenario: Incomplete listing metadata

- GIVEN a listing omits an object's modification time or version
- WHEN retention evaluates the object
- THEN it MUST retain the object.

#### Scenario: Concurrent replacement

- GIVEN an expired object is replaced after listing
- WHEN the conditional delete rejects the listed version
- THEN retention MUST preserve the replacement and continue processing other objects.

#### Scenario: Adapter lacks metadata maintenance

- GIVEN a blob adapter cannot list metadata or delete a listed version conditionally
- WHEN retention starts
- THEN it MUST report the unsupported capability without falling back to content reads.
