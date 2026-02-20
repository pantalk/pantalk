# Twilio Setup

Pantalk connects to Twilio using the **REST API**, polling for incoming SMS/MMS messages and sending outbound messages via the Messages resource. No public URL or webhook endpoint is needed.

## Prerequisites

- A Twilio account ([sign up at twilio.com](https://www.twilio.com/try-twilio))
- A Twilio phone number capable of sending/receiving SMS
- Your Pantalk binaries installed (`pantalk` and `pantalkd`)

## Step 1 - Get Your Twilio Credentials

1. Log in to the [Twilio Console](https://console.twilio.com/)
2. On the dashboard, find your **Account SID** and **Auth Token**
3. Copy both values — you'll need them for configuration

> **Note:** The Account SID starts with `AC` and the Auth Token is a 32-character hex string.

## Step 2 - Get a Twilio Phone Number

If you don't already have one:

1. Go to **Phone Numbers** → **Manage** → **Buy a number** in the Twilio Console
2. Choose a number with SMS capability
3. Note the phone number in E.164 format (e.g. `+15551234567`)

If you already have a number, find it under **Phone Numbers** → **Manage** → **Active numbers**.

## Step 3 - Configure Pantalk

Set your environment variables:

```bash
export TWILIO_AUTH_TOKEN="your-auth-token-here"
export TWILIO_ACCOUNT_SID="ACxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxx"
```

Add the bot to your Pantalk config:

```yaml
bots:
  - name: my-twilio-bot
    type: twilio
    auth_token: $TWILIO_AUTH_TOKEN
    account_sid: $TWILIO_ACCOUNT_SID
    phone_number: '+15551234567'          # your Twilio phone number (E.164)
    channels:
      - '+15559876543'                # limit to specific phone numbers (optional)
```

### Config Fields

| Field             | Maps To             | Description                                  |
| ----------------- | ------------------- | -------------------------------------------- |
| `auth_token`      | Twilio Auth Token   | Secret credential for API authentication     |
| `account_sid`     | Twilio Account SID  | Account identifier (starts with `AC`)        |
| `phone_number`    | Twilio Phone Number | Your Twilio number in E.164 format           |
| `channels`        | Allowed Numbers     | Optional: limit to specific phone numbers    |

> **Note:** If `channels` is empty, the bot will accept messages from all phone numbers. Specify numbers to restrict which contacts can reach the bot.

## Verify

Start the daemon and check that the bot connects:

```bash
pantalkd &
pantalk bots
```

You should see your Twilio bot listed. Send a test message:

```bash
pantalk send --bot my-twilio-bot --channel +15559876543 --text "Hello from Pantalk!"
```

## Troubleshooting

| Symptom                        | Cause                                                                         |
| ------------------------------ | ----------------------------------------------------------------------------- |
| `401 Unauthorized`             | Invalid Auth Token or Account SID — check credentials in Twilio Console       |
| `21608` error                  | Phone number not owned by account — verify the number in Active Numbers       |
| `21211` error                  | Invalid `To` number — ensure E.164 format with country code (e.g. `+1...`)   |
| Connected but no messages      | Messages may take 5-10s to appear due to polling interval                     |
| Trial account limitations      | Trial accounts can only send to verified numbers — upgrade or verify numbers  |
