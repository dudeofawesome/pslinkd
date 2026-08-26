Run non-basic commands from inside the devenv (`devenv shell --quiet -- {command}`), which will not work in a sandbox.

# Spec-driven development

## General Rules

- Always read files in /specs before implementing
- Never implement without acceptance criteria

## Required Workflow

1. Read the specs in the /specs directory
2. Generate tasks.md if it does not exist
3. Implement based on the tasks
4. Create automated tests where relevant
5. Ensure all acceptance criteria pass

## Testing

- Prioritize coverage of acceptance criteria
- Tests should be clear and straightforward

## Constraints

- Do not invent requirements that are not described
- Do not change behavior without updating the spec

## Architecture

specified in specs/architecture.md
