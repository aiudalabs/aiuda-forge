# Skill — frontend-quality

React/TypeScript pitfalls that repeatedly fail acceptance review. Check each before
you finish; they map to real criteria, not style preferences.

## State coverage
- **Empty state**: when a list/query returns `[]`, render the placeholder the AC asks
  for — do NOT hide the section. A hidden section is not an empty state.
- **Loading state**: show the skeleton/spinner the design implies while a query is
  pending; don't flash empty then content.
- **Error state**: a failed query must surface an error UI, not a blank or a crash.

## Timing & freshness
- If an AC says data updates "within N seconds", a `refetchInterval`/poll MUST be ≤ N
  (and account for render). 10s does not meet "within 5s". Prefer invalidating the query
  on the mutation when "immediately/within seconds" is required.

## Forms & inputs
- Use the repo's form stack (React Hook Form + Zod), not ad-hoc `useState`.
- Inputs are **controlled**; validation messages render on the field the AC names.
- A filter/search form must NOT clear the OTHER active filters on submit.

## Enumerated controls
- If the AC lists controls ("filter by category, minimum rating, AND coverage area"),
  render an input for EACH. Cross them off one by one — a missing control is a failed AC.

## Lists, pagination, routing
- Honor the AC's page size and "paginated"/"load more" wording exactly.
- A "full profile page" / detail route must render at its own URL and handle the
  not-found case.

## Tests
- Component test per conditional branch (empty/loading/error/populated).
- Integration test (mock the data SDK) for pages that fetch.
- The test asserts the REQUIRED behavior from the AC — never what the code happens to do.
