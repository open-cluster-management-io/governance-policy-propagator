# v0.6.0

* Added concurrency to improve performance. The
  `CONTROLLER_CONFIG_CONCURRENCY_PER_POLICY` environment variable can be set to
  configure this to something other than the default of `5`.
* Improved the API validation on `PlacementBinding`, `Policy`, and `PolicyAutomation` objects.
