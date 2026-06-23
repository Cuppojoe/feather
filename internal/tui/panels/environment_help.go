package panels

// environmentHelpText is the body of the Environment variables help section.
// It covers the ${name} template syntax and the surfaces that resolve it.
const environmentHelpText = `# Environment variables

An environment is a named bundle of variables. One is active at a time
(press E to create, switch, or edit them). Reference a variable as
'${name}' and feather substitutes its value at send time, so the same
request adapts to whichever environment is active.

## Where references resolve

'${name}' is expanded in:

  - Header values (request headers and operation overrides)
  - Query parameter values
  - Path parameter values
  - The profile's base URL

References are stored verbatim and resolved when a request is sent, so
what you save stays readable. An unknown name is left as literal
'${name}' so you can spot the typo in the URL preview.

## Examples

A single Authorization header that follows the active environment:

    Authorization: Bearer ${apiToken}

A base URL switched per environment:

    https://${host}/v1

## Nested references

A variable's value can reference another variable. They resolve before
substitution, so you can compose values:

    host    = api.${region}.example.com
    region  = us-east-1
    // ${host} resolves to api.us-east-1.example.com

If a reference cycle is detected (e.g. a -> b -> a), environment variables involved in the cycle will not be expanded

## Sensitive values

Mark a variable sensitive to mask it behind bullets in the editor.
Masking is display-only; the value is still sent and saved normally.

## Scripts

Scripts read and write the same variables through
feather.environment.get / set / delete. See the Scripts help section.
`
