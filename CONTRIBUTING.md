# Contributing

We welcome contributions to the cron-operator project! Whether you're fixing bugs, adding features, or improving documentation, your contributions help make this project better.

## Development Setup

1. **Prerequisites**:
   - Go version v1.24.6 or higher
   - Docker version 17.03 or higher
   - kubectl version v1.11.3 or higher
   - Access to a Kubernetes cluster (v1.11.3+)
   - Make utility installed

2. **Clone the Repository**:

   ```sh
   git clone https://github.com/AliyunContainerService/cron-operator.git
   cd cron-operator
   ```

3. **Install Dependencies**:

   ```sh
   go mod download
   ```

4. **Run Tests**:

   ```sh
   make test        # Run unit tests
   make test-e2e    # Run end-to-end tests
   ```

## Code Structure

- `api/v1alpha1/`: CRD type definitions and API specifications
- `internal/controller/`: Controller reconciliation logic and utilities
- `cmd/main.go`: Application entry point and controller setup
- `config/`: Kubernetes manifests for CRDs, RBAC, and deployment
- `test/`: Unit tests and end-to-end test suites
- `pkg/common/`: Shared constants and utilities

## Development Workflow

1. **Create a Feature Branch**:

   ```sh
   git checkout -b feature/your-feature-name
   ```

2. **Make Your Changes**:
   - Write clean, well-documented code
   - Follow existing code patterns and conventions
   - Add tests for new functionality
   - Update documentation as needed

3. **Run Linters and Tests**:

   ```sh
   make lint        # Run golangci-lint checks
   make test        # Run unit tests
   ```

4. **Commit Your Changes**:
   - Use conventional commit format (e.g., `feat:`, `fix:`, `docs:`)
   - Include DCO sign-off with `-s` flag:

     ```sh
     git commit -s -m "feat: add new feature description"
     ```

   - The sign-off certifies that you have the right to submit the code under the project's license

5. **Push and Create Pull Request**:

   ```sh
   git push origin feature/your-feature-name
   ```

   - Open a pull request against the `main` branch
   - Provide a clear description of your changes
   - Reference any related issues

## Testing Requirements

- **Unit Tests**: All new code should include unit tests with reasonable coverage
- **Integration Tests**: Consider adding integration tests for controller logic in `internal/controller/*_test.go`
- **E2E Tests**: For significant features, add end-to-end tests in `test/e2e/`
- **Test Execution**: Ensure all tests pass before submitting PR:

  ```sh
  make test
  ```

## Code Style

- Follow Go best practices and idiomatic patterns
- Code must pass `golangci-lint` checks (see `.golangci.yml`)
- Add meaningful comments for exported functions and types
- Use descriptive variable and function names

## Pull Request Guidelines

- Keep PRs focused on a single feature or fix
- Write clear PR titles following conventional commit format
- Include detailed description of changes and motivation
- Respond to review feedback promptly
- Ensure CI checks pass before requesting review
- Squash commits before merging if requested

## Getting Help

If you have questions or need assistance:

- Open an issue for bug reports or feature requests
- Check existing issues and PRs for similar discussions
- Review the [Kubebuilder Documentation](https://book.kubebuilder.io/introduction.html) for operator development patterns

Thank you for contributing to cron-operator!
