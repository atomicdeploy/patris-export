# TODO List

This document tracks planned features and enhancements for the Patris Export project.

## High Priority

### Core Functionality
- [ ] Add comprehensive test coverage for all packages
  - [ ] Unit tests for converter package
  - [ ] Integration tests with sample database files
  - [ ] WebSocket connection tests
  - [ ] API endpoint tests

- [ ] Performance optimization
  - [ ] Benchmark large database file processing
  - [ ] Optimize memory usage for large datasets
  - [ ] Add streaming support for very large files
  - [ ] Implement connection pooling for database access

- [ ] Error handling improvements
  - [ ] Better error messages with suggestions
  - [ ] Recovery from partial file reads
  - [ ] Validation of database file integrity

### Documentation
- [ ] Add code documentation (godoc)
- [ ] Create detailed API documentation
- [ ] Add usage examples and tutorials
- [ ] Document character encoding mappings
- [ ] Create troubleshooting guide

## Medium Priority

### Features
- [ ] Support for additional export formats
  - [ ] SQL INSERT statements
  - [ ] XML format
  - [ ] Excel (XLSX) format
  - [ ] SQLite database export

- [ ] Advanced filtering and transformation
  - [ ] Field selection (export only specific columns)
  - [ ] Record filtering (WHERE-like conditions)
  - [ ] Data transformation pipelines
  - [ ] Custom field mappings

- [ ] Batch processing
  - [ ] Process multiple files at once
  - [ ] Directory watching (watch all .db files in a directory)
  - [ ] Parallel processing for multiple files

- [ ] Database diff functionality
  - [ ] Compare two database files
  - [ ] Show added/removed/changed records
  - [ ] Export diff to various formats

### API Enhancements
- [ ] GraphQL API support
- [ ] Advanced WebSocket features
  - [ ] Subscribe to specific tables/records
  - [ ] Filter updates by criteria
  - [ ] Authentication and authorization
  - [ ] Rate limiting

- [ ] RESTful improvements
  - [ ] Pagination for large result sets
  - [ ] Sorting and ordering
  - [ ] Field filtering in responses
  - [ ] Caching headers and ETag support

### Configuration
- [ ] Configuration file support
  - [ ] YAML configuration
  - [ ] JSON configuration
  - [ ] Environment variable support
  - [ ] Multi-environment configs (dev, prod)

## Low Priority

### Developer Experience
- [ ] Docker containerization
  - [ ] Dockerfile for easy deployment
  - [ ] Docker Compose for development
  - [ ] Multi-stage builds for smaller images

- [ ] Development tools
  - [ ] Hot reload during development
  - [ ] Debug mode with extra logging
  - [ ] Profiling support

### Advanced Features
- [ ] Import capabilities
  - [ ] Convert JSON/CSV back to Paradox format
  - [ ] Migrate from other database formats

- [ ] Data validation
  - [ ] Schema validation
  - [ ] Data integrity checks
  - [ ] Custom validation rules

- [ ] Compression and optimization
  - [ ] Compressed JSON/CSV output
  - [ ] Incremental exports (only changed records)
  - [ ] Delta updates via WebSocket

- [ ] Security features
  - [ ] API authentication (JWT, OAuth)
  - [ ] Access control for specific tables/fields
  - [ ] Audit logging
  - [ ] Data encryption in transit

### Platform Support
- [ ] macOS build support
- [ ] ARM64 builds (Linux and macOS)
- [ ] FreeBSD support
- [ ] Static binary builds

### UI/UX
- [ ] Web-based GUI for management
  - [ ] File upload and conversion
  - [ ] Real-time data viewer
  - [ ] Export history and logs
  
- [ ] Interactive TUI (Terminal UI)
  - [ ] Browse database records in terminal
  - [ ] Interactive field selection
  - [ ] Live update visualization

### Infrastructure
- [ ] Monitoring and metrics
  - [ ] Prometheus metrics endpoint
  - [ ] Performance metrics
  - [ ] Health check endpoint
  - [ ] Structured logging (JSON logs)

- [ ] Scalability
  - [ ] Horizontal scaling support
  - [ ] Load balancing
  - [ ] Distributed file watching
  - [ ] Message queue integration

## Future Considerations

### Research & Exploration
- [ ] Support for other Paradox versions
- [ ] Support for other BDE database formats
- [ ] Machine learning for encoding detection
- [ ] Automatic schema inference
- [ ] Natural language query interface

### Integration
- [ ] Database connectors (MySQL, PostgreSQL, etc.)
- [ ] Cloud storage support (S3, Azure Blob, GCS)
- [ ] Webhook notifications
- [ ] Integration with ETL tools
- [ ] Plugin system for custom exporters

## Completed
- [x] Basic Paradox database reading
- [x] Persian/Farsi character conversion
- [x] JSON export
- [x] CSV export
- [x] File watching with hash detection
- [x] REST API server
- [x] WebSocket real-time updates
- [x] CLI with colorful output
- [x] Company.inf file parsing
- [x] GitHub Actions CI/CD
- [x] Linux and Windows builds
- [x] Comprehensive README

---

**Note:** This TODO list is a living document. Items may be added, removed, or reprioritized based on user feedback and project needs.
