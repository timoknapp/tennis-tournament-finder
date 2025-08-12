# BoltDB Cache Implementation

This document describes the persistent caching system implemented for the tennis-tournament-finder application.

## Overview

The application now supports persistent caching using BoltDB while maintaining backward compatibility with the existing in-memory caching system. The cache can operate in two modes:

1. **Memory + Persistence Mode** (default): Uses in-memory maps for fast access with BoltDB write-through for persistence
2. **BoltDB-only Mode**: Uses BoltDB exclusively for both reads and writes

## Configuration

The caching system is controlled by two environment variables:

### `TTF_CACHE_MEMORY`
- **Default**: `true`
- **Values**: `true` or `false`
- **Description**: Controls whether in-memory caching is enabled
  - `true`: Enables in-memory maps with BoltDB write-through
  - `false`: Uses BoltDB exclusively for all cache operations

### `TTF_CACHE_PATH`
- **Default**: `./data/cache.bolt`
- **Description**: Path to the BoltDB cache file
- **Note**: Directory will be created automatically if it doesn't exist

## Cache Key Structure

The system maintains the existing cache key conventions:

- **Tournament Cache**: `{tournament.Id}`
- **Location Cache**: `loc:{normalized_location}:{state}`
- **Organizer Cache**: `org:{normalized_organizer}:{state}`

## Architecture

### Components

1. **`pkg/cache`**: New package containing the BoltDB-backed Store interface
2. **`pkg/openstreetmap`**: Modified to use the unified cache system
3. **Graceful Shutdown**: Proper resource cleanup when application terminates

### Cache Operations

- **Memory Mode**: Fast in-memory access with async persistence to BoltDB
- **BoltDB Mode**: Direct database operations for all cache access
- **Preloading**: When memory mode is enabled, existing BoltDB data is loaded into memory at startup
- **Write-through**: All cache writes are persisted to BoltDB regardless of memory mode

## Performance Characteristics

### Memory Mode (TTF_CACHE_MEMORY=true)
- **Read Speed**: Very fast (in-memory access)
- **Write Speed**: Fast (in-memory + async BoltDB write)
- **Memory Usage**: Higher (stores cache in RAM)
- **Persistence**: Yes (via BoltDB write-through)

### BoltDB Mode (TTF_CACHE_MEMORY=false)
- **Read Speed**: Moderate (disk access)
- **Write Speed**: Moderate (disk access)
- **Memory Usage**: Lower (no in-memory cache)
- **Persistence**: Yes (direct BoltDB operations)

## Usage Examples

### Default Configuration (Memory + Persistence)
```bash
# Uses in-memory cache with BoltDB persistence
./main
```

### Memory-disabled Configuration
```bash
# Uses only BoltDB for caching
TTF_CACHE_MEMORY=false ./main
```

### Custom Cache Path
```bash
# Uses custom cache file location
TTF_CACHE_PATH=/var/cache/tennis/cache.bolt ./main
```

### Combined Configuration
```bash
# Memory disabled with custom path
TTF_CACHE_MEMORY=false TTF_CACHE_PATH=/var/cache/tennis/cache.bolt ./main
```

## Cache Statistics

The system provides detailed cache statistics accessible via the `/` endpoint:

- `total_entries`: Total number of cached entries
- `successful`: Entries with valid geocoordinates
- `failed`: Entries with failed geocoding attempts
- `pending_retry`: Failed entries eligible for retry
- `permanently_failed`: Failed entries beyond retry limit
- `location_cache_size`: Number of location-based cache entries
- `organizer_cache_size`: Number of organizer-based cache entries
- `tournament_cache_size`: Number of tournament-specific cache entries

## Error Handling

The system includes robust error handling:

- **BoltDB Initialization Failures**: Falls back to memory-only mode
- **Read/Write Errors**: Logged but don't crash the application
- **Corrupted Cache Data**: Individual entries are skipped, iteration continues
- **Disk Space Issues**: Gracefully handled with appropriate error logging

## Migration

The system is designed for seamless migration:

1. **Fresh Installation**: Works immediately with default settings
2. **Existing Installations**: Backward compatible - existing in-memory behavior preserved
3. **Gradual Migration**: Can enable persistence without changing behavior
4. **Performance Testing**: Easy to compare memory vs. BoltDB-only performance

## Maintenance

### Cache Cleanup
- Old failed entries (30+ days, 4+ failures) are automatically cleaned up
- Cleanup triggers when cache has >1000 entries and >100 permanently failed entries

### Monitoring
- Cache statistics are logged on every API request
- Initialization status is logged at startup
- Error conditions are logged with appropriate detail levels