# API Specification

This document provides complete API specifications for all endpoints in the project.

## Base URL

Replace `{backend_url}` with your backend URL (e.g., `http://localhost:8080/api/v1`)

---

## Health Check

### Check Server Health

**Endpoint:** `GET /health`

**Description:** Health check endpoint to verify server is running.

**Request:**

```bash
curl -X GET {backend_url}/../health
```

**Response:**

```json
{
  "status": "ok",
  "message": "Server is running"
}
```

---

## User Management

### 1. Create User

**Endpoint:** `POST {backend_url}/users`

**Description:** Creates a new user in the system.

**Request:**

```bash
curl -X POST {backend_url}/users \
  -H "Content-Type: application/json" \
  -d '{
    "name": "John Doe",
    "country_code": "+1",
    "phone": "1234567890",
    "email": "john@example.com",
    "app_name": "krush_connect",
    "metadata": {
      "signup_source": "mobile_app"
    }
  }'
```

**Request Body:**
| Field | Type | Required | Description |
|-------|------|----------|-------------|
| name | string | No | User's name |
| country_code | string | No | Country code (e.g., +1) |
| phone | string | No | Phone number |
| email | string | No | Email address (validated) |
| app_name | string | Yes | Application name (max 100 chars) |
| metadata | object | No | Additional user metadata |

**Response:**

```json
{
  "data": {
    "id": "123e4567-e89b-12d3-a456-426614174000",
    "name": "John Doe",
    "country_code": "+1",
    "phone": "1234567890",
    "email": "john@example.com",
    "app_name": "krush_connect",
    "metadata": {
      "signup_source": "mobile_app"
    },
    "created_at": "2025-12-30T10:00:00Z",
    "updated_at": "2025-12-30T10:00:00Z"
  }
}
```

### 2. Get User by ID

**Endpoint:** `GET {backend_url}/users/:id`

**Description:** Retrieves a user by their UUID.

**Request:**

```bash
curl -X GET {backend_url}/users/123e4567-e89b-12d3-a456-426614174000
```

**Response:**

```json
{
  "data": {
    "id": "123e4567-e89b-12d3-a456-426614174000",
    "name": "John Doe",
    "country_code": "+1",
    "phone": "1234567890",
    "email": "john@example.com",
    "app_name": "krush_connect",
    "metadata": {},
    "created_at": "2025-12-30T10:00:00Z",
    "updated_at": "2025-12-30T10:00:00Z"
  }
}
```

### 3. Get User by Phone

**Endpoint:** `GET {backend_url}/users/by-phone`

**Description:** Retrieves a user by app name and phone number.

**Request:**

```bash
curl -X GET "{backend_url}/users/by-phone?app_name=krush_connect&country_code=%2B1&phone=1234567890"
```

**Query Parameters:**
| Parameter | Type | Required | Description |
|-----------|------|----------|-------------|
| app_name | string | Yes | Application name |
| country_code | string | Yes | Country code (URL encoded) |
| phone | string | Yes | Phone number |

**Response:**

```json
{
  "data": {
    "id": "123e4567-e89b-12d3-a456-426614174000",
    "name": "John Doe",
    "country_code": "+1",
    "phone": "1234567890",
    "email": "john@example.com",
    "app_name": "krush_connect",
    "metadata": {},
    "created_at": "2025-12-30T10:00:00Z",
    "updated_at": "2025-12-30T10:00:00Z"
  }
}
```

### 4. Get User by Email

**Endpoint:** `GET {backend_url}/users/by-email`

**Description:** Retrieves a user by app name and email.

**Request:**

```bash
curl -X GET "{backend_url}/users/by-email?app_name=krush_connect&email=john@example.com"
```

**Query Parameters:**
| Parameter | Type | Required | Description |
|-----------|------|----------|-------------|
| app_name | string | Yes | Application name |
| email | string | Yes | Email address |

**Response:**

```json
{
  "data": {
    "id": "123e4567-e89b-12d3-a456-426614174000",
    "name": "John Doe",
    "country_code": "+1",
    "phone": "1234567890",
    "email": "john@example.com",
    "app_name": "krush_connect",
    "metadata": {},
    "created_at": "2025-12-30T10:00:00Z",
    "updated_at": "2025-12-30T10:00:00Z"
  }
}
```

### 5. Update User

**Endpoint:** `PUT {backend_url}/users/:id`

**Description:** Updates an existing user.

**Request:**

```bash
curl -X PUT {backend_url}/users/123e4567-e89b-12d3-a456-426614174000 \
  -H "Content-Type: application/json" \
  -d '{
    "name": "John Updated",
    "metadata": {
      "last_login": "2025-12-30"
    }
  }'
```

**Request Body:**
| Field | Type | Required | Description |
|-------|------|----------|-------------|
| name | string | No | User's name |
| country_code | string | No | Country code |
| phone | string | No | Phone number |
| email | string | No | Email address |
| app_name | string | No | Application name |
| metadata | object | No | Additional user metadata |

**Response:**

```json
{
  "data": {
    "id": "123e4567-e89b-12d3-a456-426614174000",
    "name": "John Updated",
    "country_code": "+1",
    "phone": "1234567890",
    "email": "john@example.com",
    "app_name": "krush_connect",
    "metadata": {
      "last_login": "2025-12-30"
    },
    "created_at": "2025-12-30T10:00:00Z",
    "updated_at": "2025-12-30T11:00:00Z"
  }
}
```

### 6. List All Users

**Endpoint:** `GET {backend_url}/users/all`

**Description:** Retrieves a paginated list of all users.

**Request:**

```bash
curl -X GET "{backend_url}/users/all?page=1&page_size=10&app_name=krush_connect"
```

**Query Parameters:**
| Parameter | Type | Required | Description |
|-----------|------|----------|-------------|
| page | integer | No | Page number (default: 1) |
| page_size | integer | No | Items per page (default: 10) |
| app_name | string | No | Filter by app name |

**Response:**

```json
{
  "data": [
    {
      "id": "123e4567-e89b-12d3-a456-426614174000",
      "name": "John Doe",
      "country_code": "+1",
      "phone": "1234567890",
      "email": "john@example.com",
      "app_name": "krush_connect",
      "metadata": {},
      "crushes_count": 3,
      "created_at": "2025-12-30T10:00:00Z",
      "updated_at": "2025-12-30T10:00:00Z"
    }
  ],
  "page": 1,
  "page_size": 10,
  "total": 50,
  "total_pages": 5,
  "next_page": 2,
  "prev_page": null
}
```

**Response Fields:**
| Field | Type | Description |
|-------|------|-------------|
| crushes_count | integer | Number of crushes created by this user |

---

## OTP Management

### Phone OTP

#### 1. Create or Update Phone OTP

**Endpoint:** `POST {backend_url}/otp/phone`

**Description:** Generates and sends a 6-digit OTP to a phone number via SMS.

**Request:**

```bash
curl -X POST {backend_url}/otp/phone \
  -H "Content-Type: application/json" \
  -d '{
    "app_name": "krush_connect",
    "country_code": "+1",
    "phone": "1234567890"
  }'
```

**Request Body:**
| Field | Type | Required | Description |
|-------|------|----------|-------------|
| app_name | string | Yes | Application name |
| country_code | string | Yes | Country code |
| phone | string | Yes | Phone number |

**Response:**

```json
{
  "data": {
    "expires_at": "2025-12-30T10:05:00Z"
  }
}
```

#### 2. Verify Phone OTP

**Endpoint:** `POST {backend_url}/otp/phone/verify`

**Description:** Verifies the OTP sent to a phone number.

**Request:**

```bash
curl -X POST {backend_url}/otp/phone/verify \
  -H "Content-Type: application/json" \
  -d '{
    "app_name": "krush_connect",
    "country_code": "+1",
    "phone": "1234567890",
    "value": "123456"
  }'
```

**Request Body:**
| Field | Type | Required | Description |
|-------|------|----------|-------------|
| app_name | string | Yes | Application name |
| country_code | string | Yes | Country code |
| phone | string | Yes | Phone number |
| value | string | Yes | 6-digit OTP code |

**Response (Success):**

```json
{
  "data": {
    "valid": true,
    "message": "OTP verified successfully"
  }
}
```

**Response (Failed):**

```json
{
  "data": {
    "valid": false,
    "message": "invalid or expired OTP"
  }
}
```

### Email OTP

#### 1. Create or Update Email OTP

**Endpoint:** `POST {backend_url}/otp/email`

**Description:** Generates and sends a 6-digit OTP to an email address.

**Request:**

```bash
curl -X POST {backend_url}/otp/email \
  -H "Content-Type: application/json" \
  -d '{
    "app_name": "krush_connect",
    "email": "john@example.com"
  }'
```

**Request Body:**
| Field | Type | Required | Description |
|-------|------|----------|-------------|
| app_name | string | Yes | Application name |
| email | string | Yes | Email address (validated) |

**Response:**

```json
{
  "data": {
    "expires_at": "2025-12-30T10:05:00Z"
  }
}
```

#### 2. Verify Email OTP

**Endpoint:** `POST {backend_url}/otp/email/verify`

**Description:** Verifies the OTP sent to an email address.

**Request:**

```bash
curl -X POST {backend_url}/otp/email/verify \
  -H "Content-Type: application/json" \
  -d '{
    "app_name": "krush_connect",
    "email": "john@example.com",
    "value": "123456"
  }'
```

**Request Body:**
| Field | Type | Required | Description |
|-------|------|----------|-------------|
| app_name | string | Yes | Application name |
| email | string | Yes | Email address |
| value | string | Yes | 6-digit OTP code |

**Response (Success):**

```json
{
  "data": {
    "valid": true,
    "message": "OTP verified successfully"
  }
}
```

**Response (Failed):**

```json
{
  "data": {
    "valid": false,
    "message": "invalid or expired OTP"
  }
}
```

---

## Crush Management

### 1. Create Crush

**Endpoint:** `POST {backend_url}/crushes`

**Description:** Creates a new crush entry for a user.

**Request:**

```bash
curl -X POST {backend_url}/crushes \
  -H "Content-Type: application/json" \
  -d '{
    "user_id": "123e4567-e89b-12d3-a456-426614174000",
    "name": "Jane Doe",
    "country_code": "+1",
    "phone": "9876543210",
    "instagram_id": "@janedoe",
    "snapchat_id": "janesnap",
    "metadata": {
      "note": "Met at coffee shop"
    }
  }'
```

**Request Body:**
| Field | Type | Required | Description |
|-------|------|----------|-------------|
| user_id | UUID | Yes | ID of the user creating the crush |
| name | string | Yes | Name of the crush (1-255 chars) |
| country_code | string | No | Country code |
| phone | string | No | Phone number |
| instagram_id | string | No | Instagram username |
| snapchat_id | string | No | Snapchat username |
| metadata | object | No | Additional metadata |

**Response:**

```json
{
  "data": {
    "id": "987fcdeb-51a2-43c1-b678-123456789abc",
    "user_id": "123e4567-e89b-12d3-a456-426614174000",
    "name": "Jane Doe",
    "country_code": "+1",
    "phone": "9876543210",
    "instagram_id": "@janedoe",
    "snapchat_id": "janesnap",
    "metadata": {
      "note": "Met at coffee shop"
    },
    "created_at": "2025-12-30T10:00:00Z",
    "updated_at": "2025-12-30T10:00:00Z"
  }
}
```

### 2. Get Crush by ID

**Endpoint:** `GET {backend_url}/crushes/:id`

**Description:** Retrieves a crush by its UUID.

**Request:**

```bash
curl -X GET {backend_url}/crushes/987fcdeb-51a2-43c1-b678-123456789abc
```

**Response:**

```json
{
  "data": {
    "id": "987fcdeb-51a2-43c1-b678-123456789abc",
    "user_id": "123e4567-e89b-12d3-a456-426614174000",
    "name": "Jane Doe",
    "country_code": "+1",
    "phone": "9876543210",
    "instagram_id": "@janedoe",
    "snapchat_id": "janesnap",
    "metadata": {},
    "created_at": "2025-12-30T10:00:00Z",
    "updated_at": "2025-12-30T10:00:00Z"
  }
}
```

### 3. Update Crush

**Endpoint:** `PUT {backend_url}/crushes/:id`

**Description:** Updates an existing crush entry.

**Request:**

```bash
curl -X PUT {backend_url}/crushes/987fcdeb-51a2-43c1-b678-123456789abc \
  -H "Content-Type: application/json" \
  -d '{
    "name": "Jane Smith",
    "metadata": {
      "note": "Updated info"
    }
  }'
```

**Request Body:**
| Field | Type | Required | Description |
|-------|------|----------|-------------|
| name | string | No | Name of the crush (1-255 chars) |
| country_code | string | No | Country code |
| phone | string | No | Phone number |
| instagram_id | string | No | Instagram username |
| snapchat_id | string | No | Snapchat username |
| metadata | object | No | Additional metadata |

**Response:**

```json
{
  "data": {
    "id": "987fcdeb-51a2-43c1-b678-123456789abc",
    "user_id": "123e4567-e89b-12d3-a456-426614174000",
    "name": "Jane Smith",
    "country_code": "+1",
    "phone": "9876543210",
    "instagram_id": "@janedoe",
    "snapchat_id": "janesnap",
    "metadata": {
      "note": "Updated info"
    },
    "created_at": "2025-12-30T10:00:00Z",
    "updated_at": "2025-12-30T11:00:00Z"
  }
}
```

### 4. List Crushes by User

**Endpoint:** `GET {backend_url}/crushes`

**Description:** Retrieves all crushes created by a specific user.

**Request:**

```bash
curl -X GET "{backend_url}/crushes?user_id=123e4567-e89b-12d3-a456-426614174000"
```

**Query Parameters:**
| Parameter | Type | Required | Description |
|-----------|------|----------|-------------|
| user_id | UUID | Yes | ID of the user |

**Response:**

```json
{
  "data": [
    {
      "id": "987fcdeb-51a2-43c1-b678-123456789abc",
      "user_id": "123e4567-e89b-12d3-a456-426614174000",
      "name": "Jane Doe",
      "country_code": "+1",
      "phone": "9876543210",
      "instagram_id": "@janedoe",
      "snapchat_id": "janesnap",
      "metadata": {},
      "created_at": "2025-12-30T10:00:00Z",
      "updated_at": "2025-12-30T10:00:00Z"
    }
  ]
}
```

### 5. List Crushes on User

**Endpoint:** `GET {backend_url}/crushes/on-user`

**Description:** Retrieves all crushes that have been created with this user's contact information.

**Request:**

```bash
curl -X GET "{backend_url}/crushes/on-user?user_id=123e4567-e89b-12d3-a456-426614174000"
```

**Query Parameters:**
| Parameter | Type | Required | Description |
|-----------|------|----------|-------------|
| user_id | UUID | Yes | ID of the user |

**Response:**

```json
{
  "data": [
    {
      "created_at": "2025-12-30T10:00:00Z"
    },
    {
      "created_at": "2025-12-29T15:30:00Z"
    }
  ]
}
```

### 6. List All Crushes (Admin)

**Endpoint:** `GET {backend_url}/crushes/all`

**Description:** Retrieves a paginated list of all crushes (admin view with minimal data).

**Request:**

```bash
curl -X GET "{backend_url}/crushes/all?page=1&page_size=10"
```

**Query Parameters:**
| Parameter | Type | Required | Description |
|-----------|------|----------|-------------|
| page | integer | No | Page number (default: 1) |
| page_size | integer | No | Items per page (default: 10) |

**Response:**

```json
{
  "data": [
    {
      "user_country_code": "+1",
      "user_phone": "1234567890",
      "crush_country_code": "+1",
      "crush_phone": "9876543210",
      "instagram_id": "@janedoe",
      "snapchat_id": "janesnap",
      "created_at": "2025-12-30T10:00:00Z"
    }
  ],
  "page": 1,
  "page_size": 10,
  "total": 100,
  "total_pages": 10,
  "next_page": 2,
  "prev_page": null
}
```

---

## Razorpay Configuration

### 1. Create Razorpay Config

**Endpoint:** `POST {backend_url}/razorpay-configs`

**Description:** Creates a new Razorpay configuration for an app and environment.

**Request:**

```bash
curl -X POST {backend_url}/razorpay-configs \
  -H "Content-Type: application/json" \
  -d '{
    "app_name": "krush_connect",
    "environment": "test",
    "razorpay_key_id": "rzp_test_xxxxxxxxxxxxx",
    "razorpay_key_secret": "your_secret_key",
    "razorpay_webhook_secret": "your_webhook_secret",
    "is_active": true,
    "metadata": {}
  }'
```

**Request Body:**
| Field | Type | Required | Description |
|-------|------|----------|-------------|
| app_name | string | Yes | Application name (1-100 chars) |
| environment | string | Yes | Either "test" or "live" |
| razorpay_key_id | string | Yes | Razorpay key ID |
| razorpay_key_secret | string | Yes | Razorpay key secret |
| razorpay_webhook_secret | string | Yes | Razorpay webhook secret |
| is_active | boolean | No | Whether config is active (default: true) |
| metadata | object | No | Additional metadata |

**Response:**

```json
{
  "id": "456e7890-e12b-34d5-a678-426614174999",
  "app_name": "krush_connect",
  "environment": "test",
  "is_active": true,
  "metadata": {},
  "created_at": "2025-12-30T10:00:00Z",
  "updated_at": "2025-12-30T10:00:00Z"
}
```

### 2. Get Razorpay Config by ID

**Endpoint:** `GET {backend_url}/razorpay-configs/:id`

**Description:** Retrieves a Razorpay configuration by its UUID.

**Request:**

```bash
curl -X GET {backend_url}/razorpay-configs/456e7890-e12b-34d5-a678-426614174999
```

**Response:**

```json
{
  "id": "456e7890-e12b-34d5-a678-426614174999",
  "app_name": "krush_connect",
  "environment": "test",
  "is_active": true,
  "metadata": {},
  "created_at": "2025-12-30T10:00:00Z",
  "updated_at": "2025-12-30T10:00:00Z"
}
```

### 3. Get Razorpay Config by App and Environment

**Endpoint:** `GET {backend_url}/razorpay-configs/by-app`

**Description:** Retrieves a Razorpay configuration by app name and environment.

**Request:**

```bash
curl -X GET "{backend_url}/razorpay-configs/by-app?app_name=krush_connect&environment=test"
```

**Query Parameters:**
| Parameter | Type | Required | Description |
|-----------|------|----------|-------------|
| app_name | string | Yes | Application name |
| environment | string | No | Either "test" or "live" (default: "test") |

**Response:**

```json
{
  "id": "456e7890-e12b-34d5-a678-426614174999",
  "app_name": "krush_connect",
  "environment": "test",
  "is_active": true,
  "metadata": {},
  "created_at": "2025-12-30T10:00:00Z",
  "updated_at": "2025-12-30T10:00:00Z"
}
```

### 4. Get All Razorpay Configs

**Endpoint:** `GET {backend_url}/razorpay-configs`

**Description:** Retrieves a paginated list of all Razorpay configurations.

**Request:**

```bash
curl -X GET "{backend_url}/razorpay-configs?page=1&page_size=10&active_only=true"
```

**Query Parameters:**
| Parameter | Type | Required | Description |
|-----------|------|----------|-------------|
| page | integer | No | Page number (default: 1) |
| page_size | integer | No | Items per page (default: 10) |
| active_only | boolean | No | Filter only active configs (default: false) |

**Response:**

```json
{
  "data": [
    {
      "id": "456e7890-e12b-34d5-a678-426614174999",
      "app_name": "krush_connect",
      "environment": "test",
      "is_active": true,
      "metadata": {},
      "created_at": "2025-12-30T10:00:00Z",
      "updated_at": "2025-12-30T10:00:00Z"
    }
  ],
  "page": 1,
  "page_size": 10,
  "total": 5,
  "total_pages": 1,
  "next_page": null,
  "prev_page": null
}
```

### 5. Update Razorpay Config

**Endpoint:** `PUT {backend_url}/razorpay-configs/:id`

**Description:** Updates an existing Razorpay configuration.

**Request:**

```bash
curl -X PUT {backend_url}/razorpay-configs/456e7890-e12b-34d5-a678-426614174999 \
  -H "Content-Type: application/json" \
  -d '{
    "is_active": false,
    "metadata": {
      "note": "Deprecated"
    }
  }'
```

**Request Body:**
| Field | Type | Required | Description |
|-------|------|----------|-------------|
| razorpay_key_id | string | No | Razorpay key ID |
| razorpay_key_secret | string | No | Razorpay key secret |
| razorpay_webhook_secret | string | No | Razorpay webhook secret |
| is_active | boolean | No | Whether config is active |
| metadata | object | No | Additional metadata |

**Response:**

```json
{
  "id": "456e7890-e12b-34d5-a678-426614174999",
  "app_name": "krush_connect",
  "environment": "test",
  "is_active": false,
  "metadata": {
    "note": "Deprecated"
  },
  "created_at": "2025-12-30T10:00:00Z",
  "updated_at": "2025-12-30T11:00:00Z"
}
```

### 6. Delete Razorpay Config

**Endpoint:** `DELETE {backend_url}/razorpay-configs/:id`

**Description:** Soft deletes a Razorpay configuration.

**Request:**

```bash
curl -X DELETE {backend_url}/razorpay-configs/456e7890-e12b-34d5-a678-426614174999
```

**Response:**

```json
{
  "message": "razorpay config deleted successfully"
}
```

---

## Razorpay Subscriptions

### 1. Create Checkout URL

**Endpoint:** `POST {backend_url}/subscriptions/checkout`

**Description:** Creates a UPI Autopay subscription and returns a checkout URL.

**Request:**

```bash
curl -X POST {backend_url}/subscriptions/checkout \
  -H "Content-Type: application/json" \
  -d '{
    "user_id": "123e4567-e89b-12d3-a456-426614174000",
    "app_name": "krush_connect",
    "phone": "1234567890",
    "email": "john@example.com",
    "plan_id": "plan_xxxxxxxxxxxxx",
    "total_count": 12,
    "quantity": 1,
    "initial_charge_amount": 1,
    "first_charge_delay_days": 1,
    "notes": {
      "subscription_type": "premium"
    }
  }'
```

**Request Body:**
| Field | Type | Required | Description |
|-------|------|----------|-------------|
| user_id | UUID | Yes | User ID |
| app_name | string | Yes | Application name (1-100 chars) |
| phone | string | Yes | Phone number (10-15 chars) |
| email | string | Yes | Email address (validated) |
| plan_id | string | Yes | Razorpay plan ID |
| total_count | integer | No | Total number of charges |
| start_at | integer | No | Unix timestamp for subscription start |
| quantity | integer | No | Quantity (default: 1) |
| initial_charge_amount | integer | No | Initial charge in rupees (default: 1) |
| first_charge_delay_days | integer | No | Days to delay first recurring charge (default: 1) |
| notes | object | No | Additional notes |
| client_id | UUID | No | Razorpay config ID (auto-derived if not provided) |

**Response:**

```json
{
  "data": {
    "subscription_id": "789abc12-3def-4567-8901-234567890def",
    "razorpay_subscription_id": "sub_xxxxxxxxxxxxx",
    "short_url": "https://rzp.io/i/xxxxxxxxx",
    "status": "created"
  }
}
```

### 2. Verify Payment

**Endpoint:** `POST {backend_url}/subscriptions/verify`

**Description:** Verifies payment signature after successful payment.

**Request:**

```bash
curl -X POST {backend_url}/subscriptions/verify \
  -H "Content-Type: application/json" \
  -d '{
    "razorpay_subscription_id": "sub_xxxxxxxxxxxxx",
    "razorpay_payment_id": "pay_xxxxxxxxxxxxx",
    "razorpay_signature": "generated_signature_string"
  }'
```

**Request Body:**
| Field | Type | Required | Description |
|-------|------|----------|-------------|
| razorpay_subscription_id | string | Yes | Razorpay subscription ID |
| razorpay_payment_id | string | Yes | Razorpay payment ID |
| razorpay_signature | string | Yes | Payment signature |

**Response:**

```json
{
  "data": {
    "id": "789abc12-3def-4567-8901-234567890def",
    "user_id": "123e4567-e89b-12d3-a456-426614174000",
    "app_name": "krush_connect",
    "phone": "1234567890",
    "email": "john@example.com",
    "razorpay_subscription_id": "sub_xxxxxxxxxxxxx",
    "razorpay_customer_id": "cust_xxxxxxxxxxxxx",
    "status": "authenticated",
    "amount": 10000,
    "currency": "INR",
    "short_url": "https://rzp.io/i/xxxxxxxxx",
    "next_charge_at": "2025-12-31T10:00:00Z",
    "created_at": "2025-12-30T10:00:00Z",
    "updated_at": "2025-12-30T10:05:00Z"
  },
  "message": "payment verified successfully"
}
```

### 3. Handle Webhook

**Endpoint:** `POST {backend_url}/subscriptions/webhook`

**Description:** Receives and processes Razorpay webhook events.

**Request:**

```bash
curl -X POST {backend_url}/subscriptions/webhook \
  -H "Content-Type: application/json" \
  -H "X-Razorpay-Signature: webhook_signature" \
  -d '{
    "event": "subscription.activated",
    "payload": {
      "subscription": {
        "entity": {
          "id": "sub_xxxxxxxxxxxxx",
          "status": "active"
        }
      }
    }
  }'
```

**Headers:**
| Header | Required | Description |
|--------|----------|-------------|
| X-Razorpay-Signature | Yes | Webhook signature for verification |

**Response:**

```json
{
  "message": "webhook processed successfully"
}
```

### 4. Get Subscription by ID

**Endpoint:** `GET {backend_url}/subscriptions/:id`

**Description:** Retrieves subscription details by UUID.

**Request:**

```bash
curl -X GET {backend_url}/subscriptions/789abc12-3def-4567-8901-234567890def
```

**Response:**

```json
{
  "data": {
    "id": "789abc12-3def-4567-8901-234567890def",
    "user_id": "123e4567-e89b-12d3-a456-426614174000",
    "app_name": "krush_connect",
    "phone": "1234567890",
    "email": "john@example.com",
    "razorpay_subscription_id": "sub_xxxxxxxxxxxxx",
    "razorpay_customer_id": "cust_xxxxxxxxxxxxx",
    "status": "active",
    "amount": 10000,
    "currency": "INR",
    "short_url": "https://rzp.io/i/xxxxxxxxx",
    "next_charge_at": "2025-12-31T10:00:00Z",
    "created_at": "2025-12-30T10:00:00Z",
    "updated_at": "2025-12-30T10:05:00Z"
  }
}
```

### 5. Get Subscription by Razorpay ID

**Endpoint:** `GET {backend_url}/subscriptions/razorpay/:razorpay_id`

**Description:** Retrieves subscription details by Razorpay subscription ID.

**Request:**

```bash
curl -X GET {backend_url}/subscriptions/razorpay/sub_xxxxxxxxxxxxx
```

**Response:**

```json
{
  "data": {
    "id": "789abc12-3def-4567-8901-234567890def",
    "user_id": "123e4567-e89b-12d3-a456-426614174000",
    "app_name": "krush_connect",
    "phone": "1234567890",
    "email": "john@example.com",
    "razorpay_subscription_id": "sub_xxxxxxxxxxxxx",
    "razorpay_customer_id": "cust_xxxxxxxxxxxxx",
    "status": "active",
    "amount": 10000,
    "currency": "INR",
    "created_at": "2025-12-30T10:00:00Z",
    "updated_at": "2025-12-30T10:05:00Z"
  }
}
```

### 6. Get Latest Subscription by Phone and App

**Endpoint:** `GET {backend_url}/subscriptions/latest`

**Description:** Retrieves the latest subscription for a phone number and app.

**Request:**

```bash
curl -X GET "{backend_url}/subscriptions/latest?phone=1234567890&app_name=krush_connect"
```

**Query Parameters:**
| Parameter | Type | Required | Description |
|-----------|------|----------|-------------|
| phone | string | Yes | Phone number |
| app_name | string | Yes | Application name |

**Response:**

```json
{
  "data": {
    "id": "789abc12-3def-4567-8901-234567890def",
    "user_id": "123e4567-e89b-12d3-a456-426614174000",
    "app_name": "krush_connect",
    "phone": "1234567890",
    "email": "john@example.com",
    "razorpay_subscription_id": "sub_xxxxxxxxxxxxx",
    "razorpay_customer_id": "cust_xxxxxxxxxxxxx",
    "status": "active",
    "amount": 10000,
    "currency": "INR",
    "created_at": "2025-12-30T10:00:00Z",
    "updated_at": "2025-12-30T10:05:00Z"
  }
}
```

### 7. Cancel Subscription

**Endpoint:** `POST {backend_url}/subscriptions/:id/cancel`

**Description:** Cancels an active subscription.

**Request:**

```bash
curl -X POST {backend_url}/subscriptions/789abc12-3def-4567-8901-234567890def/cancel
```

**Response:**

```json
{
  "message": "subscription cancelled successfully"
}
```

### 8. Check Authentication Status

**Endpoint:** `GET {backend_url}/subscriptions/check-authentication`

**Description:** Checks if a phone number has ever had an authenticated subscription.

**Request:**

```bash
curl -X GET "{backend_url}/subscriptions/check-authentication?phone=1234567890&app_name=krush_connect"
```

**Query Parameters:**
| Parameter | Type | Required | Description |
|-----------|------|----------|-------------|
| phone | string | Yes | Phone number |
| app_name | string | No | Application name (optional filter) |

**Response:**

```json
{
  "data": {
    "has_authenticated": true,
    "phone": "1234567890"
  }
}
```

---

## DailyStory - Image Templates

### 1. Create Image Template

**Endpoint:** `POST {backend_url}/dailystory/image-templates`

**Description:** Creates a new image template entry in the database.

**Request:**

```bash
curl -X POST {backend_url}/dailystory/image-templates \
  -H "Content-Type: application/json" \
  -d '{
    "file_key": "images/template_1735560000.png",
    "category": "birthday",
    "sub_category": "cake",
    "config": {
      "face": {
        "center_x": 250.5,
        "center_y": 180.3,
        "radius": 75.0
      },
      "name": {
        "top_left_x": 50.0,
        "top_left_y": 400.0,
        "width": 200.0,
        "height": 50.0
      }
    },
    "metadata": {
      "tags": ["celebration", "fun"]
    },
    "author_id": "123e4567-e89b-12d3-a456-426614174000"
  }'
```

**Request Body:**
| Field | Type | Required | Description |
|-------|------|----------|-------------|
| file_key | string | Yes | R2 file key (path in bucket) |
| category | string | Yes | Template category |
| sub_category | string | Yes | Template sub-category |
| config | object | No | Face and text positioning config |
| metadata | object | No | Additional metadata |
| author_id | UUID | No | Template author ID |

**Response:**

```json
{
  "data": {
    "id": "111e2222-e33b-44d5-a666-777788889999",
    "file_key": "images/template_1735560000.png",
    "category": "birthday",
    "sub_category": "cake",
    "config": {
      "face": {
        "center_x": 250.5,
        "center_y": 180.3,
        "radius": 75.0
      },
      "name": {
        "top_left_x": 50.0,
        "top_left_y": 400.0,
        "width": 200.0,
        "height": 50.0
      }
    },
    "metadata": {
      "tags": ["celebration", "fun"]
    },
    "author_id": "123e4567-e89b-12d3-a456-426614174000",
    "created_at": "2025-12-30T10:00:00Z",
    "updated_at": "2025-12-30T10:00:00Z"
  }
}
```

### 2. Get Image Template by ID

**Endpoint:** `GET {backend_url}/dailystory/image-templates/:id`

**Description:** Retrieves an image template by its UUID.

**Request:**

```bash
curl -X GET {backend_url}/dailystory/image-templates/111e2222-e33b-44d5-a666-777788889999
```

**Response:**

```json
{
  "data": {
    "id": "111e2222-e33b-44d5-a666-777788889999",
    "file_key": "images/template_1735560000.png",
    "category": "birthday",
    "sub_category": "cake",
    "config": {
      "face": {
        "center_x": 250.5,
        "center_y": 180.3,
        "radius": 75.0
      },
      "name": {
        "top_left_x": 50.0,
        "top_left_y": 400.0,
        "width": 200.0,
        "height": 50.0
      }
    },
    "metadata": {},
    "author_id": "123e4567-e89b-12d3-a456-426614174000",
    "created_at": "2025-12-30T10:00:00Z",
    "updated_at": "2025-12-30T10:00:00Z"
  }
}
```

### 3. Update Image Template

**Endpoint:** `PUT {backend_url}/dailystory/image-templates/:id`

**Description:** Updates an existing image template.

**Request:**

```bash
curl -X PUT {backend_url}/dailystory/image-templates/111e2222-e33b-44d5-a666-777788889999 \
  -H "Content-Type: application/json" \
  -d '{
    "category": "birthday",
    "sub_category": "party",
    "metadata": {
      "tags": ["celebration", "fun", "party"]
    }
  }'
```

**Request Body:**
| Field | Type | Required | Description |
|-------|------|----------|-------------|
| file_key | string | No | R2 file key |
| category | string | No | Template category |
| sub_category | string | No | Template sub-category |
| config | object | No | Face and text positioning config |
| metadata | object | No | Additional metadata |
| author_id | UUID | No | Template author ID |

**Response:**

```json
{
  "data": {
    "id": "111e2222-e33b-44d5-a666-777788889999",
    "file_key": "images/template_1735560000.png",
    "category": "birthday",
    "sub_category": "party",
    "config": {
      "face": {
        "center_x": 250.5,
        "center_y": 180.3,
        "radius": 75.0
      },
      "name": {
        "top_left_x": 50.0,
        "top_left_y": 400.0,
        "width": 200.0,
        "height": 50.0
      }
    },
    "metadata": {
      "tags": ["celebration", "fun", "party"]
    },
    "author_id": "123e4567-e89b-12d3-a456-426614174000",
    "created_at": "2025-12-30T10:00:00Z",
    "updated_at": "2025-12-30T11:00:00Z"
  }
}
```

### 4. Get Image Templates (List with Filters)

**Endpoint:** `GET {backend_url}/dailystory/image-templates`

**Description:** Retrieves a paginated list of image templates with optional filters.

**Request:**

```bash
curl -X GET "{backend_url}/dailystory/image-templates?category=birthday&sub_category=cake&status=published&page=1&page_size=10"
```

**Query Parameters:**
| Parameter | Type | Required | Description |
|-----------|------|----------|-------------|
| category | string | No | Filter by category |
| sub_category | string | No | Filter by sub-category |
| author_id | UUID | No | Filter by author ID |
| status | string | No | Filter by status ('published', 'approved', or 'rejected') |
| page | integer | No | Page number (default: 1) |
| page_size | integer | No | Items per page (default: 10) |

**Response:**

```json
{
  "data": [
    {
      "id": "111e2222-e33b-44d5-a666-777788889999",
      "file_key": "images/template_1735560000.png",
      "category": "birthday",
      "sub_category": "cake",
      "config": {
        "face": {
          "center_x": 250.5,
          "center_y": 180.3,
          "radius": 75.0
        }
      },
      "metadata": {},
      "author_id": "123e4567-e89b-12d3-a456-426614174000",
      "created_at": "2025-12-30T10:00:00Z",
      "updated_at": "2025-12-30T10:00:00Z"
    }
  ],
  "page": 1,
  "page_size": 10,
  "total": 25,
  "total_pages": 3,
  "next_page": 2,
  "prev_page": null
}
```

### 5. Get Upload URL for Template

**Endpoint:** `POST {backend_url}/dailystory/image-templates/upload-url`

**Description:** Generates a presigned URL for uploading an image template to R2.

**Request:**

```bash
curl -X POST {backend_url}/dailystory/image-templates/upload-url \
  -H "Content-Type: application/json" \
  -d '{
    "filename": "birthday_cake.png"
  }'
```

**Request Body:**
| Field | Type | Required | Description |
|-------|------|----------|-------------|
| filename | string | Yes | Original filename with extension |

**Response:**

```json
{
  "presigned_url": "https://account.r2.cloudflarestorage.com/bucket/images/birthday_cake_1735560000.png?X-Amz-Algorithm=...",
  "file_key": "images/birthday_cake_1735560000.png",
  "upload_headers": {
    "Content-Type": "image/png"
  },
  "instructions": "MUST send Content-Type: image/png header when uploading. The presigned URL signature requires this exact header."
}
```

### 6. Get Image Template View URL

**Endpoint:** `GET {backend_url}/dailystory/image-templates/:id/view-url`

**Description:** Gets the public URL to view/access an image template.

**Request:**

```bash
curl -X GET {backend_url}/dailystory/image-templates/111e2222-e33b-44d5-a666-777788889999/view-url
```

**Response:**

```json
{
  "view_url": "https://pub-xxxxx.r2.dev/images/template_1735560000.png",
  "file_key": "images/template_1735560000.png"
}
```

### 7. Get Designer Stats

**Endpoint:** `GET {backend_url}/dailystory/image-templates/designer-stats`

**Description:** Retrieves template creation statistics for all designers.

**Request:**

```bash
curl -X GET {backend_url}/dailystory/image-templates/designer-stats
```

**Response:**

```json
{
  "data": [
    {
      "user_id": "123e4567-e89b-12d3-a456-426614174000",
      "user_name": "John Doe",
      "templates_created_today": 5,
      "templates_created_this_week": 23,
      "templates_created_this_month": 87,
      "templates_created_total": 150,
      "templates_pending_approval": 3
    },
    {
      "user_id": "987fcdeb-51a2-43c1-b678-123456789abc",
      "user_name": "Jane Smith",
      "templates_created_today": 2,
      "templates_created_this_week": 15,
      "templates_created_this_month": 45,
      "templates_created_total": 98,
      "templates_pending_approval": 1
    }
  ]
}
```

**Response Fields:**
| Field | Type | Description |
|-------|------|-------------|
| user_id | UUID | Designer's user ID |
| user_name | string | Designer's name (may be null) |
| templates_created_today | integer | Number of templates created today |
| templates_created_this_week | integer | Number of templates created this week |
| templates_created_this_month | integer | Number of templates created this month |
| templates_created_total | integer | Total number of templates created |
| templates_pending_approval | integer | Number of templates awaiting approval |

---

## DailyStory - Profile Pictures

### 1. Get Upload URL for Profile Picture

**Endpoint:** `POST {backend_url}/dailystory/profile-picture/upload-url`

**Description:** Generates a presigned URL for uploading a profile picture to R2.

**Request:**

```bash
curl -X POST {backend_url}/dailystory/profile-picture/upload-url \
  -H "Content-Type: application/json" \
  -d '{
    "filename": "user_avatar.jpg"
  }'
```

**Request Body:**
| Field | Type | Required | Description |
|-------|------|----------|-------------|
| filename | string | Yes | Original filename with extension |

**Response:**

```json
{
  "presigned_url": "https://account.r2.cloudflarestorage.com/bucket/profile-pictures/user_avatar_1735560000.jpg?X-Amz-Algorithm=...",
  "file_key": "profile-pictures/user_avatar_1735560000.jpg",
  "upload_headers": {
    "Content-Type": "image/jpeg"
  },
  "instructions": "MUST send Content-Type: image/jpeg header when uploading. The presigned URL signature requires this exact header."
}
```

### 2. Get Profile Picture View URL

**Endpoint:** `GET {backend_url}/dailystory/profile-picture/view-url`

**Description:** Gets a presigned URL to view/access a profile picture (valid for 60 minutes).

**Request:**

```bash
curl -X GET "{backend_url}/dailystory/profile-picture/view-url?file_key=profile-pictures/user_avatar_1735560000.jpg"
```

**Query Parameters:**
| Parameter | Type | Required | Description |
|-----------|------|----------|-------------|
| file_key | string | Yes | File key (must start with "profile-pictures/") |

**Response:**

```json
{
  "view_url": "https://account.r2.cloudflarestorage.com/bucket/profile-pictures/user_avatar_1735560000.jpg?X-Amz-Algorithm=...",
  "file_key": "profile-pictures/user_avatar_1735560000.jpg",
  "expiry_time": 1735567200000
}
```

**Response Fields:**
| Field | Type | Description |
|-------|------|-------------|
| view_url | string | Presigned URL to view/download the profile picture |
| file_key | string | File key in R2 bucket |
| expiry_time | int64 | Unix timestamp in milliseconds when the URL expires |

---

## DailyStory - Image Posters

### 1. Generate Poster

**Endpoint:** `POST {backend_url}/dailystory/posters/generate`

**Description:** Generates a personalized poster by combining an image template with user's profile picture and name. Returns a cached poster URL if one already exists for the same combination of user, template, name, and profile picture.

**Request:**

```bash
curl -X POST {backend_url}/dailystory/posters/generate \
  -H "Content-Type: application/json" \
  -d '{
    "template_id": "111e2222-e33b-44d5-a666-777788889999",
    "user_id": "123e4567-e89b-12d3-a456-426614174000"
  }'
```

**Request Body:**
| Field | Type | Required | Description |
|-------|------|----------|-------------|
| template_id | UUID | Yes | ID of the image template |
| user_id | UUID | Yes | ID of the user |

**Response (New Poster):**

```json
{
  "data": {
    "poster_url": "https://pub-xxxxx.r2.dev/images/a1b2c3d4_1735560000.png",
    "cached": false
  }
}
```

**Response (Cached Poster):**

```json
{
  "data": {
    "poster_url": "https://pub-xxxxx.r2.dev/images/a1b2c3d4_1735560000.png",
    "cached": true
  }
}
```

**Response Fields:**
| Field | Type | Description |
|-------|------|-------------|
| poster_url | string | Public URL of the generated poster image |
| cached | boolean | Whether the poster was retrieved from cache (true) or newly generated (false) |

**Notes:**

- User must have `profile_picture_key` stored in their metadata field (e.g., `{"profile_picture_key": "profile-pictures/user_avatar_1735560000.jpg"}`)
- Template must have complete configuration with `face` and `name` settings
- Image processing uses RAM only (no disk I/O)
- Memory usage is logged during generation
- Posters are cached based on the unique combination of: user_id, template_id, user's name, and user's profile picture key
- Generated posters are stored in the `R2_DS_POSTERS_BUCKET_NAME` bucket

**Error Responses:**

404 Not Found:

```json
{
  "error": "user not found"
}
```

```json
{
  "error": "template not found"
}
```

400 Bad Request:

```json
{
  "error": "user does not have a profile picture"
}
```

```json
{
  "error": "template does not have complete configuration"
}
```

---

## Error Responses

All endpoints may return the following error responses:

### 400 Bad Request

```json
{
  "error": "validation error message"
}
```

### 404 Not Found

```json
{
  "error": "resource not found"
}
```

### 409 Conflict

```json
{
  "error": "resource already exists"
}
```

### 500 Internal Server Error

```json
{
  "error": "internal server error message"
}
```

---

## Notes

### Authentication

Currently, the API does not implement authentication. This should be added before production deployment.

### File Upload Flow (R2)

1. Call the upload URL endpoint to get a presigned URL
2. Use HTTP PUT to upload the file directly to R2 with the required Content-Type header
3. After successful upload, create a database record with the returned file_key

### Subscription Workflow

1. Create checkout URL → Returns short_url
2. User completes payment on Razorpay
3. Razorpay sends webhook events
4. Optionally verify payment signature
5. Check subscription status

### Metadata Fields

Most entities support a flexible `metadata` JSONB field for storing custom key-value pairs.

### Pagination

Paginated endpoints return:

- `data`: Array of results
- `page`: Current page number
- `page_size`: Items per page
- `total`: Total number of items
- `total_pages`: Total number of pages
- `next_page`: Next page number (null if last page)
- `prev_page`: Previous page number (null if first page)
