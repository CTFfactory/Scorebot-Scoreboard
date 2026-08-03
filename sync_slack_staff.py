#!/usr/bin/env python3
import json
import urllib.request
import urllib.parse
import os
import re

CHANNEL_NAME = "bslv2026_all_pvj_staff"
IMAGE_DIR = "scoreboard/html/public/image/credit"
OUTPUT_HTML_FILE = "staff_credits.html"

def get_slack_token():
    """Reads the Slack token from the local Slack CLI credentials file."""
    creds_path = os.path.expanduser("~/.slack/credentials.json")
    if not os.path.exists(creds_path):
        print(f"Error: Slack CLI credentials file not found at {creds_path}")
        return None
    
    try:
        with open(creds_path, "r") as f:
            creds = json.load(f)
        
        # Get the first available token
        for team_id, team_info in creds.items():
            if "token" in team_info:
                return team_info["token"]
    except Exception as e:
        print(f"Error reading credentials file: {e}")
        
    return None

def make_slack_request(method, token, params=None):
    """Makes a request to the Slack Web API using urllib."""
    url = f"https://slack.com/api/{method}"
    
    if params:
        data = urllib.parse.urlencode(params).encode("utf-8")
        req = urllib.request.Request(url, data=data, method="POST")
    else:
        req = urllib.request.Request(url, method="GET")
        
    req.add_header("Authorization", f"Bearer {token}")
    req.add_header("Content-Type", "application/x-www-form-urlencoded")
    
    try:
        with urllib.request.urlopen(req) as response:
            res_data = response.read().decode("utf-8")
            return json.loads(res_data)
    except Exception as e:
        print(f"API request to {method} failed: {e}")
        return None

def main():
    token = get_slack_token()
    if not token:
        print("Error: Could not retrieve Slack API token from Slack CLI credentials.")
        return

    os.makedirs(IMAGE_DIR, exist_ok=True)
    
    print("Searching for channel ID...")
    # List channels (both public and private)
    channels_data = make_slack_request("conversations.list", token, {
        "types": "public_channel,private_channel",
        "limit": 1000
    })
    
    if not channels_data or not channels_data.get("ok"):
        print(f"Error listing channels: {channels_data.get('error') if channels_data else 'unknown'}")
        return

    channel_id = None
    for channel in channels_data.get("channels", []):
        if channel["name"] == CHANNEL_NAME:
            channel_id = channel["id"]
            break

    if not channel_id:
        print(f"Error: Could not find channel #{CHANNEL_NAME}. Make sure you have joined/joined the channel.")
        return

    print(f"Found channel #{CHANNEL_NAME} with ID: {channel_id}")
    print("Fetching channel members...")
    
    members_data = make_slack_request("conversations.members", token, {
        "channel": channel_id,
        "limit": 1000
    })
    
    if not members_data or not members_data.get("ok"):
        print(f"Error fetching channel members: {members_data.get('error') if members_data else 'unknown'}")
        return

    member_ids = members_data.get("members", [])
    print(f"Found {len(member_ids)} members. Fetching profiles...")

    html_elements = []

    for index, uid in enumerate(member_ids, 1):
        print(f"[{index}/{len(member_ids)}] Fetching user {uid}...")
        user_data = make_slack_request("users.info", token, {"user": uid})
        if not user_data or not user_data.get("ok"):
            continue
        
        user = user_data["user"]
        if user.get("is_bot") or user.get("deleted"):
            continue

        profile = user.get("profile", {})
        # Use display name if set, otherwise real name
        name = profile.get("display_name") or profile.get("real_name") or user.get("name")
        image_url = profile.get("image_192") or profile.get("image_72")
        
        if not name or not image_url:
            continue

        # Sanitize name for filename
        safe_name = re.sub(r'[^a-zA-Z0-9_-]', '_', name).lower()
        image_ext = "png"
        if ".jpg" in image_url or ".jpeg" in image_url:
            image_ext = "jpg"
        
        filename = f"slack_{safe_name}.{image_ext}"
        filepath = os.path.join(IMAGE_DIR, filename)

        # Download avatar image
        try:
            print(f"  Downloading avatar to {filepath}...")
            urllib.request.urlretrieve(image_url, filepath)
        except Exception as e:
            print(f"  Failed to download avatar for {name}: {e}")
            continue

        # Generate HTML snippet for this user
        html_link = f'                        <a rel="noopener" target="_blank" href="#">\n' \
                    f'                            <img src="/image/credit/{filename}" alt="{name}" />\n' \
                    f'                            {name}\n' \
                    f'                        </a>'
        html_elements.append(html_link)

    # Write HTML output to a file
    final_html = "\n".join(html_elements)
    with open(OUTPUT_HTML_FILE, "w") as f:
        f.write(final_html)

    print("\n" + "="*50)
    print(f"Success! Downloaded avatars to: {IMAGE_DIR}")
    print(f"Generated HTML snippet saved to: {OUTPUT_HTML_FILE}")
    print("="*50)

if __name__ == "__main__":
    main()
