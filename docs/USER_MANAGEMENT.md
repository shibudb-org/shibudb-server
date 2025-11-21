# ShibuDb User Management Guide

## Table of Contents

- [Overview](#overview)
- [Authentication System](#authentication-system)
- [User Roles](#user-roles)
- [User Management Commands](#user-management-commands)
- [Permission System](#permission-system)
- [Security Best Practices](#security-best-practices)
- [Examples and Use Cases](#examples-and-use-cases)
- [Troubleshooting](#troubleshooting)

## Overview

ShibuDb provides a comprehensive user management system with role-based access control (RBAC) and fine-grained permissions. The system supports multiple user roles and space-specific permissions to ensure secure access to database resources.

### Key Features

- **Role-Based Access Control**: Admin and User roles with different privileges
- **Space-Level Permissions**: Fine-grained control over individual spaces
- **Secure Authentication**: Password-based authentication with role validation
- **User Lifecycle Management**: Create, update, and delete users
- **Permission Inheritance**: Role-based default permissions with space-specific overrides

### Security Architecture

```
┌─────────────────────────────────────┐
│         Authentication Layer        │
├─────────────────────────────────────┤
│  Username/Password Validation       │
├─────────────────────────────────────┤
│         Authorization Layer         │
├─────────────────────────────────────┤
│  Role-Based Access Control (RBAC)   │
├─────────────────────────────────────┤
│      Permission Enforcement         │
├─────────────────────────────────────┤
│  Space-Level Permission Checks      │
└─────────────────────────────────────┘
```

## Authentication System

### Default Admin User

On first startup, ShibuDb creates a default admin user:

- **Username**: `admin`
- **Password**: `admin`
- **Role**: `admin`
- **Permissions**: Full access to all spaces

### Login Process

```bash
# Connect to ShibuDb
shibudb connect 9090

# You'll be prompted for credentials
Username: admin
Password: admin

# Successful login response
Login successful.
[]>
```

### Authentication Flow

1. **Connection**: Client connects to server
2. **Login Request**: Client sends username/password
3. **Validation**: Server validates credentials
4. **Role Assignment**: Server assigns user role and permissions
5. **Session**: Client can now execute commands based on permissions

## User Roles

### Admin Role

**Privileges:**
- Create and delete spaces
- Create, update, and delete users
- Full access to all spaces (read/write)
- Manage user permissions
- Access to all system commands

**Use Case**: System administrators, database owners

### User Role

**Privileges:**
- Access to spaces based on permissions
- Read/write operations on permitted spaces
- Cannot create or delete spaces
- Cannot manage other users

**Use Case**: Application users, developers, analysts

### Role Comparison

| Feature | Admin | User |
|---------|-------|------|
| Create Spaces | ✅ | ❌ |
| Delete Spaces | ✅ | ❌ |
| Create Users | ✅ | ❌ |
| Update Users | ✅ | ❌ |
| Delete Users | ✅ | ❌ |
| List Spaces | ✅ | ✅ |
| Use Spaces | ✅ | ✅ |
| Read Data | ✅ | ✅ (permitted spaces) |
| Write Data | ✅ | ✅ (permitted spaces) |

## User Management Commands

### CREATE-USER - Create New User

**Admin Only**

```bash
# Create a new user (interactive)
CREATE-USER

# System will prompt for:
# - Username
# - Password
# - Role (admin/user)
# - Permissions (if user role)
```

**Example Session:**
```bash
[admin]> CREATE-USER
New Username: john_doe
New Password: secure_password123
Role (admin/user): user
Enter table permissions (e.g., table1=read or table2=write). Leave blank to finish:
Permission: users=write
Permission: products=read
Permission: 
Success: USER_CREATED
```

### UPDATE-USER-PASSWORD - Change Password

**Admin Only**

```bash
# Update user password
UPDATE-USER-PASSWORD <username>

# System will prompt for new password
```

**Example:**
```bash
[admin]> UPDATE-USER-PASSWORD john_doe
New Password: new_secure_password456
Success: USER_PASSWORD_UPDATED
```

### UPDATE-USER-ROLE - Change User Role

**Admin Only**

```bash
# Update user role
UPDATE-USER-ROLE <username>

# System will prompt for new role
```

**Example:**
```bash
[admin]> UPDATE-USER-ROLE john_doe
Role (admin/user): admin
Success: USER_ROLE_UPDATED
```

### UPDATE-USER-PERMISSIONS - Modify Permissions

**Admin Only**

```bash
# Update user permissions
UPDATE-USER-PERMISSIONS <username>

# System will prompt for new permissions
```

**Example:**
```bash
[admin]> UPDATE-USER-PERMISSIONS john_doe
Enter table permissions (e.g., table1=read or table2=write). Leave blank to finish:
Permission: users=write
Permission: products=read
Permission: analytics=write
Permission: 
Success: USER_ROLE_UPDATED
```

### DELETE-USER - Remove User

**Admin Only**

```bash
# Delete a user
DELETE-USER <username>
```

**Example:**
```bash
[admin]> DELETE-USER john_doe
Success: USER_DELETED
```

### GET-USER - View User Information

**Admin Only**

```bash
# Get user details
GET-USER <username>
```

**Example:**
```bash
[admin]> GET-USER john_doe
Username: john_doe | Role: user | Permissions: users=write, products=read
```

## Permission System

### Permission Types

#### Read Permission (`read`)
- **Allowed Operations**: GET, GET-VECTOR, SEARCH-TOPK, RANGE-SEARCH
- **Use Case**: Read-only access to data

#### Write Permission (`write`)
- **Allowed Operations**: PUT, DELETE, INSERT-VECTOR, GET, GET-VECTOR, SEARCH-TOPK, RANGE-SEARCH
- **Use Case**: Full read/write access to data

### Permission Format

```
<space_name>=<permission_type>
```

**Examples:**
- `users=read` - Read-only access to users space
- `products=write` - Read/write access to products space
- `analytics=write` - Read/write access to analytics space

### Permission Inheritance

#### Admin Role
- **Default**: Full access to all spaces
- **Override**: Cannot be restricted by space permissions

#### User Role
- **Default**: No access to any spaces
- **Override**: Must be explicitly granted permissions per space

### Permission Examples

#### Read-Only User
```bash
# User with read-only access
Username: analyst
Role: user
Permissions: users=read, products=read, analytics=read

# Can perform:
GET user:123
SEARCH-TOPK 1.0,2.0,3.0 5
GET-VECTOR 1

# Cannot perform:
PUT user:123 "new_value"
DELETE user:123
INSERT-VECTOR 1 1.0,2.0,3.0
```

#### Write-Access User
```bash
# User with write access
Username: developer
Role: user
Permissions: users=write, products=write

# Can perform:
PUT user:123 "new_value"
DELETE user:123
INSERT-VECTOR 1 1.0,2.0,3.0
GET user:123
SEARCH-TOPK 1.0,2.0,3.0 5

# Cannot access:
# analytics space (no permission)
```

#### Admin User
```bash
# Admin with full access
Username: admin
Role: admin
Permissions: None (full access by default)

# Can perform:
# All operations on all spaces
CREATE-SPACE new_space --engine key-value
DELETE-SPACE old_space
CREATE-USER new_user
PUT user:123 "value"
GET user:123
```

## Security Best Practices

### 1. Password Security

#### Strong Password Requirements
```bash
# Good passwords
secure_password_2024!
MyApp@Database#123
Complex_P@ssw0rd_2024

# Avoid weak passwords
password
123456
admin
```

#### Password Rotation
```bash
# Regular password updates
UPDATE-USER-PASSWORD admin
# Change default admin password monthly

UPDATE-USER-PASSWORD john_doe
# Change user passwords quarterly
```

### 2. User Role Management

#### Principle of Least Privilege
```bash
# Grant minimal necessary permissions
# Instead of: users=write, products=write, analytics=write
# Use: users=read, products=write

# Create specific roles for different use cases
# Analyst role: read-only access
# Developer role: write access to specific spaces
# Admin role: full access
```

#### Regular Access Reviews
```bash
# Review user permissions regularly
GET-USER john_doe
GET-USER analyst_1
GET-USER developer_2

# Remove unnecessary permissions
UPDATE-USER-PERMISSIONS john_doe
# Remove unused space permissions
```

### 3. Space-Level Security

#### Separate Spaces by Function
```bash
# Create separate spaces for different data types
CREATE-SPACE user_data --engine key-value
CREATE-SPACE product_catalog --engine key-value
CREATE-SPACE analytics_data --engine vector

# Grant permissions accordingly
# User management team: user_data=write
# Product team: product_catalog=write
# Analytics team: analytics_data=write
```

#### Sensitive Data Isolation
```bash
# Create isolated spaces for sensitive data
CREATE-SPACE sensitive_users --engine key-value
CREATE-SPACE financial_data --engine key-value

# Grant access only to authorized users
# UPDATE-USER-PERMISSIONS authorized_user
# Permission: sensitive_users=read
```

### 4. Authentication Security

#### Change Default Credentials
```bash
# Immediately after first login
UPDATE-USER-PASSWORD admin
# Change from default 'admin' password
```

#### Monitor Login Attempts
```bash
# Check server logs for authentication failures
tail -f /usr/local/var/log/shibudb.log | grep "authentication failed"
```

### 5. Network Security

#### Firewall Configuration
```bash
# Restrict access to ShibuDb ports
sudo ufw allow from 192.168.1.0/24 to any port 9090
sudo ufw allow from 192.168.1.0/24 to any port 10090

# Block external access
sudo ufw deny from any to any port 9090
```

#### SSL/TLS (Future Feature)
```bash
# When SSL support is added
# Use encrypted connections for sensitive data
shibudb connect 9090 --ssl
```

## Examples and Use Cases

### 1. Multi-Tenant Application

```bash
# Create spaces for different tenants
CREATE-SPACE tenant_a_users --engine key-value
CREATE-SPACE tenant_a_products --engine key-value
CREATE-SPACE tenant_b_users --engine key-value
CREATE-SPACE tenant_b_products --engine key-value

# Create tenant-specific users
CREATE-USER tenant_a_admin
# Role: admin
# Permissions: tenant_a_users=write, tenant_a_products=write

CREATE-USER tenant_b_admin
# Role: admin
# Permissions: tenant_b_users=write, tenant_b_products=write

CREATE-USER tenant_a_user
# Role: user
# Permissions: tenant_a_users=read, tenant_a_products=read
```

### 2. Application Development Team

```bash
# Create development spaces
CREATE-SPACE dev_users --engine key-value
CREATE-SPACE dev_products --engine key-value
CREATE-SPACE dev_analytics --engine vector

# Create team roles
CREATE-USER frontend_dev
# Role: user
# Permissions: dev_users=write, dev_products=read

CREATE-USER backend_dev
# Role: user
# Permissions: dev_users=write, dev_products=write, dev_analytics=write

CREATE-USER data_scientist
# Role: user
# Permissions: dev_analytics=write, dev_users=read
```

### 3. Production Environment

```bash
# Create production spaces
CREATE-SPACE prod_users --engine key-value
CREATE-SPACE prod_products --engine key-value
CREATE-SPACE prod_analytics --engine vector

# Create production roles
CREATE-USER app_server
# Role: user
# Permissions: prod_users=write, prod_products=read

CREATE-USER analytics_service
# Role: user
# Permissions: prod_analytics=write, prod_users=read

CREATE-USER monitoring_service
# Role: user
# Permissions: prod_users=read, prod_products=read, prod_analytics=read
```

### 4. Data Science Workflow

```bash
# Create ML/AI spaces
CREATE-SPACE raw_data --engine key-value
CREATE-SPACE processed_data --engine key-value
CREATE-SPACE model_embeddings --engine vector
CREATE-SPACE predictions --engine key-value

# Create data science roles
CREATE-USER data_engineer
# Role: user
# Permissions: raw_data=write, processed_data=write

CREATE-USER ml_engineer
# Role: user
# Permissions: processed_data=read, model_embeddings=write, predictions=write

CREATE-USER data_analyst
# Role: user
# Permissions: processed_data=read, predictions=read
```

### 5. Microservices Architecture

```bash
# Create service-specific spaces
CREATE-SPACE user_service --engine key-value
CREATE-SPACE order_service --engine key-value
CREATE-SPACE recommendation_service --engine vector

# Create service accounts
CREATE-USER user_service_account
# Role: user
# Permissions: user_service=write

CREATE-USER order_service_account
# Role: user
# Permissions: order_service=write, user_service=read

CREATE-USER recommendation_service_account
# Role: user
# Permissions: recommendation_service=write, user_service=read
```

## Troubleshooting

### Common Issues

#### 1. "Only admin can create users" Error

**Problem**: Non-admin user trying to create users

**Solution**:
```bash
# Login as admin
Username: admin
Password: admin

# Then create users
CREATE-USER new_user
```

#### 2. "Write permission denied" Error

**Problem**: User doesn't have write permission for the space

**Solution**:
```bash
# Check user permissions (admin only)
GET-USER username

# Grant write permission (admin only)
UPDATE-USER-PERMISSIONS username
# Add permission: space_name=write
```

#### 3. "Read permission denied" Error

**Problem**: User doesn't have read permission for the space

**Solution**:
```bash
# Check user permissions (admin only)
GET-USER username

# Grant read permission (admin only)
UPDATE-USER-PERMISSIONS username
# Add permission: space_name=read
```

#### 4. "Authentication failed" Error

**Problem**: Invalid username or password

**Solutions**:
```bash
# Check if user exists (admin only)
GET-USER username

# Reset password (admin only)
UPDATE-USER-PASSWORD username

# Check server logs for details
tail -f /usr/local/var/log/shibudb.log
```

#### 5. "User not found" Error

**Problem**: User doesn't exist

**Solution**:
```bash
# Create the user (admin only)
CREATE-USER username

# Or check for typos in username
GET-USER username
```

### User Management Best Practices

#### Regular Maintenance

```bash
# Review all users monthly
GET-USER admin
GET-USER user1
GET-USER user2
# ... review all users

# Remove inactive users
DELETE-USER inactive_user

# Update passwords regularly
UPDATE-USER-PASSWORD admin
UPDATE-USER-PASSWORD active_user
```

#### Permission Auditing

```bash
# Audit user permissions
GET-USER user1
# Check if permissions are still needed

# Remove unnecessary permissions
UPDATE-USER-PERMISSIONS user1
# Remove unused space permissions

# Document permission changes
# Keep a log of permission modifications
```

### Security Monitoring

#### Monitor Authentication

```bash
# Watch for failed login attempts
tail -f /usr/local/var/log/shibudb.log | grep "authentication failed"

# Monitor successful logins
tail -f /usr/local/var/log/shibudb.log | grep "Login successful"
```

#### Monitor Permission Changes

```bash
# Watch for permission updates
tail -f /usr/local/var/log/shibudb.log | grep "USER_ROLE_UPDATED"

# Monitor user creation/deletion
tail -f /usr/local/var/log/shibudb.log | grep -E "(USER_CREATED|USER_DELETED)"
```

### Emergency Procedures

#### Reset Admin Password

If admin password is lost:

```bash
# Stop the server
sudo shibudb stop

# Remove users file to reset to defaults
sudo rm /usr/local/var/lib/shibudb/users.json

# Restart server (creates default admin/admin)
sudo shibudb start 9090

# Login with default credentials
Username: admin
Password: admin

# Immediately change password
UPDATE-USER-PASSWORD admin
```

#### Recover User Data

If user data is corrupted:

```bash
# Check user file integrity
cat /usr/local/var/lib/shibudb/users.json

# If corrupted, restore from backup
sudo cp /backup/users.json /usr/local/var/lib/shibudb/users.json

# Restart server
sudo shibudb stop
sudo shibudb start 9090
```

## Next Steps

After mastering User Management, explore:

- [Setup Guide](SETUP.md) - Installation and configuration
- [Key-Value Engine Guide](KEY_VALUE_ENGINE.md) - Data operations
- [Vector Engine Guide](VECTOR_ENGINE.md) - Vector search capabilities
- [Administration Guide](ADMINISTRATION.md) - Server management
- [API Reference](API_REFERENCE.md) - Complete command reference 